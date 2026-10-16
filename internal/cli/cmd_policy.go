package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// policyFetchTimeout bounds one subscription's fetch. The refresh runs detached
// from a session start, so this limits a hung source, not a waiting agent.
const policyFetchTimeout = 15 * time.Second

// policyMaxBytes bounds a fetched list. A command-policy list is a few kilobytes
// of rules; anything larger is not one.
const policyMaxBytes = 1 << 20

// policyReasonMax bounds a recorded failure reason, which a session start shows
// to the agent.
const policyReasonMax = 240

func init() {
	pol := &cobra.Command{
		Use:   "policy",
		Short: "Refresh and inspect subscribed command-policy lists",
		Long: `A repo subscribes to a shared list of command rules, from the satelle.dev
catalog or a third-party URL, with [[gate.command_policy]] in satelle.toml.
satelle ships no list: with no subscription nothing is fetched or applied.

Session start reads only the local cache and starts one detached refresh when a
list is missing or older than its max_age. A failed refresh keeps the last good
list and is named at the next session start.

A URL list provides no checksum or signature, so it is UNVERIFIED: subscribe
only to a URL you trust. A catalog list is checked against the catalog's sha256.
See 'satelle help command-rules'.`,
	}

	var server string
	refresh := &cobra.Command{
		Use:   "refresh [name...]",
		Short: "Fetch the subscribed lists into the local cache",
		Long: `Fetch each subscribed list (or the named ones), check it, and cache it.

A catalog body whose sha256 differs from the checksum the catalog returns is
refused. A list that does not parse as [[command_rule]] entries under the
command-rule grammar is refused whole. A refused or unreachable list leaves the
last good one in the cache, records the reason, and exits non-zero. A URL list
has no checksum to check and is cached as an unverified source.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPolicyRefresh(cmd, server, args)
		},
	}
	refresh.Flags().StringVar(&server, "server", "", "Hosted server URL (default: the configured server)")
	pol.AddCommand(refresh)

	pol.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show each subscription and its cached list (reads the cache only)",
		Long: `Show each [[gate.command_policy]] subscription: its source, when its list was
last fetched, how many rules it holds, and the last refresh failure. Reads the
local cache only and makes no network call.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPolicyStatus(cmd)
		},
	})
	register(pol)
}

// policySubscriptions is the process of record's subscriptions — the config the
// gate reads — and the invoking root the cache is keyed on.
func policySubscriptions() ([]config.CommandPolicy, string, error) {
	proc, invoking, _, _, err := config.LoadInvokingProcess()
	if err != nil {
		return nil, "", err
	}
	return proc.Gate.CommandPolicies, invoking, nil
}

func runPolicyRefresh(cmd *cobra.Command, serverArg string, names []string) error {
	subs, root, err := policySubscriptions()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(subs) == 0 {
		fmt.Fprintln(out, "No [[gate.command_policy]] subscriptions; nothing to refresh.")
		return nil
	}
	if len(names) > 0 {
		var picked []config.CommandPolicy
		for _, n := range names {
			found := false
			for _, s := range subs {
				if s.Name == n {
					picked, found = append(picked, s), true
				}
			}
			if !found {
				return fmt.Errorf("no command policy subscription named %q", n)
			}
		}
		subs = picked
	}
	globalDir, failed := config.GlobalDir(), 0
	for _, sub := range subs {
		ctx, cancel := context.WithTimeout(cmd.Context(), policyFetchTimeout)
		body, sum, ferr := fetchPolicy(ctx, sub, serverArg)
		cancel()
		var rules []config.CommandRule
		if ferr == nil {
			rules, ferr = parsePolicyBody(body)
		}
		if ferr != nil {
			failed++
			reason := policyReason(ferr)
			if rerr := syncstate.RecordPolicyFailure(globalDir, root, sub.Name, sub.Source(), policySourceKind(sub), reason, time.Now()); rerr != nil {
				reason += " (and the failure could not be recorded: " + rerr.Error() + ")"
			}
			fmt.Fprintf(out, "FAIL  %s: %s — last good list kept\n", sub.Name, reason)
			continue
		}
		entry := syncstate.PolicySub{Source: sub.Source(), SourceKind: policySourceKind(sub), SHA256: sum, RulesTOML: string(body)}
		if rerr := syncstate.RecordPolicy(globalDir, root, sub.Name, entry, time.Now()); rerr != nil {
			failed++
			fmt.Fprintf(out, "FAIL  %s: cache write: %v\n", sub.Name, rerr)
			continue
		}
		note := ""
		if !sub.IsCatalog() {
			note = " (" + policyUnverifiedSource + ")"
		}
		fmt.Fprintf(out, "PASS  %s: %d rule(s) from %s%s\n", sub.Name, len(rules), sub.Source(), note)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d command policy subscription(s) failed to refresh", failed, len(subs))
	}
	return nil
}

// parsePolicyBody is the one check a fetched body must pass before it is cached.
func parsePolicyBody(body []byte) ([]config.CommandRule, error) {
	if len(body) > policyMaxBytes {
		return nil, fmt.Errorf("list is larger than %d bytes", policyMaxBytes)
	}
	rules, err := config.ParseCommandPolicyList(body)
	if err != nil {
		return nil, fmt.Errorf("not a command-rule list: %w", err)
	}
	return rules, nil
}

// fetchPolicy returns the list body and its sha256. A catalog body is checked
// against the checksum the catalog returns for it, and an empty checksum is a
// failure; a URL source gives nothing to check.
func fetchPolicy(ctx context.Context, sub config.CommandPolicy, serverArg string) (body []byte, sum string, err error) {
	if !sub.IsCatalog() {
		body, err = httpGetBytes(ctx, strings.TrimSpace(sub.URL), "")
		if err != nil {
			return nil, "", err
		}
		return body, policyBodySHA256(body), nil
	}
	server := resolveServer(serverArg)
	if server == "" {
		return nil, "", errors.New("no hosted server configured — run \"satelle login\"")
	}
	client := hosted.NewClient(server, hosted.DefaultStore(), nil)
	wsID, err := client.ActiveWorkspaceID(ctx, strings.TrimSpace(sub.Workspace))
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace %q: %w", sub.Workspace, err)
	}
	body, meta, err := client.PublishedContent(ctx, wsID, strings.TrimSpace(sub.Path), 0)
	if err != nil {
		return nil, "", fmt.Errorf("fetch %s: %w", sub.Source(), err)
	}
	if parseChecksumLine(meta.BlobSHA256) == "" {
		return nil, "", errors.New("the catalog returned no checksum for the list, so it cannot be verified")
	}
	if err := verifyChecksum(body, meta.BlobSHA256); err != nil {
		return nil, "", fmt.Errorf("catalog body does not match its checksum: %w", err)
	}
	return body, policyBodySHA256(body), nil
}

func policyBodySHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// policyReason is a failure as one short line. The reason is shown to the agent
// at session start and can carry text from a remote source, so it is stripped of
// control characters and bounded.
func policyReason(err error) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, err.Error())
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > policyReasonMax {
		s = string(r[:policyReasonMax]) + "…"
	}
	return s
}

func runPolicyStatus(cmd *cobra.Command) error {
	subs, root, err := policySubscriptions()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(subs) == 0 {
		fmt.Fprintln(out, "No [[gate.command_policy]] subscriptions; no published rule applies.")
		return nil
	}
	st, _, _ := syncstate.LoadPolicy(config.GlobalDir(), root)
	now := time.Now()
	for _, sub := range subs {
		e, cached := policyCached(sub, st)
		fmt.Fprintf(out, "%s\t%s\t%s\n", sub.Name, policySourceKind(sub), sub.Source())
		switch {
		case !cached || e.RulesTOML == "":
			fmt.Fprintln(out, "  not yet fetched; no rules from it apply")
		default:
			n := 0
			if rules, perr := config.ParseCommandPolicyList([]byte(e.RulesTOML)); perr == nil {
				n = len(rules)
			}
			state := "current"
			if now.Sub(e.FetchedAt) > sub.Age() {
				state = "unverified (older than max_age " + sub.Age().String() + ")"
			}
			fmt.Fprintf(out, "  %d rule(s), last fetched %s, %s\n", n, policyTime(e.FetchedAt), state)
		}
		if e.LastError != "" {
			fmt.Fprintf(out, "  last refresh failed %s: %s\n", policyTime(e.LastErrorAt), e.LastError)
		}
		if !sub.IsCatalog() {
			fmt.Fprintf(out, "  %s\n", policyUnverifiedSource)
		}
	}
	return nil
}
