// `satelle secret push|list` manage the secrets a remote reads from satelle.dev
// (sty_0b1c72a9). A remote's definition declares each as a [[secret]] (key, scope,
// source, lifetime); push reads the value from where the declaration says, sends it
// to satelle.dev under the declared scope with the owner's CLI login, and grants it
// to the remote. list shows what satelle.dev holds, never a value. The remote reads
// its granted secrets itself, signed and bound to its run (satelle help remote).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// secretHostedClient is the signed-in client the secret and key verbs use; a test
// replaces it.
var secretHostedClient = func(server string) *hosted.Client {
	return hosted.NewClient(server, hosted.DefaultStore(), nil)
}

// secretClient is the client for the configured (or flagged) hosted server.
func secretClient(serverFlag string) (*hosted.Client, error) {
	srv := resolveServer(serverFlag)
	if srv == "" {
		return nil, errors.New("no hosted server configured — run \"satelle login\"")
	}
	return secretHostedClient(srv), nil
}

func init() {
	secret := &cobra.Command{
		Use:   "secret",
		Short: "Push a remote's declared secrets to satelle.dev and list what it holds",
		Long: `A remote declares each secret it reads as a [[secret]] in its definition: the key,
the scope it is stored under (personal or team), where its value comes from
(from = "env" reads .satelle/config/.env; from = ["cmd", ...] runs a command that
prints it) and optionally when it lapses (expires, a date, or ttl, a duration).
The definition never holds a value.

push sends each declared secret's value to satelle.dev, which holds it sealed and
returns it to no person, and grants it to the remote. The remote then reads it
when a step needs it. list shows what satelle.dev holds: key, scope, who may read
it and when it lapses, never a value. See: satelle help remote-secrets.`,
	}
	var server string
	serverFlag := func(c *cobra.Command) {
		c.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server)")
	}

	push := &cobra.Command{
		Use:   "push <remote>",
		Short: "Send the remote's declared secrets to satelle.dev and grant them to the remote",
		Long: `For each [[secret]] in the remote's definition: reads the value from .env or by
running the declared command, sends it to satelle.dev under its scope with your
satelle login, and grants it to the remote named by remote_id. A secret with
expires is sent with that date and one with ttl with now plus the ttl. Prints each
key and its outcome, never a value. Needs satelle login and remote_id set.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretPush(cmd, args[0], server)
		},
	}
	serverFlag(push)

	var workspace string
	list := &cobra.Command{
		Use:   "list [<remote>]",
		Short: "List the secrets satelle.dev holds: key, scope, readers and expiry, never a value",
		Long: `Without a remote, lists your personal secrets (and a team workspace's with
--workspace). With a remote, lists the scopes its declared secrets use. Never
prints a value: satelle.dev returns none to anyone.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runSecretList(cmd, name, workspace, server)
		},
	}
	list.Flags().StringVar(&workspace, "workspace", "", "also list this team workspace's secrets")
	serverFlag(list)

	secret.AddCommand(push, list)
	register(secret)
}

// declaredExpiry is when a freshly pushed value lapses, by the declaration: its
// expires date, or now plus its ttl; the zero time when it declares neither.
func declaredExpiry(d remotedef.SecretDecl, now time.Time) time.Time {
	if t, ok := d.ExpiresAt(); ok {
		return t
	}
	if ttl, ok := d.TTLDuration(); ok {
		return now.Add(ttl).UTC()
	}
	return time.Time{}
}

// secretFailure renders a failed secret call as the secret's key and the server's
// own code and reason.
func secretFailure(key string, err error) error {
	return fmt.Errorf("secret %s: %w", key, err)
}

// putDeclared sends one declared secret's value to satelle.dev under its scope.
func putDeclared(ctx context.Context, c *hosted.Client, d remotedef.SecretDecl, value string, now time.Time) (time.Time, error) {
	exp := declaredExpiry(d, now)
	if err := c.PutSecret(ctx, d.Scope, d.Workspace, d.Key, value, exp); err != nil {
		return time.Time{}, secretFailure(d.Key, err)
	}
	return exp, nil
}

func runSecretPush(cmd *cobra.Command, name, server string) error {
	def, err := loadRemoteDef(name)
	if err != nil {
		return err
	}
	remoteID, err := def.RequireRemoteID(name)
	if err != nil {
		return err
	}
	if !def.HasSecrets() {
		return fmt.Errorf("remote %s declares no [[secret]]; nothing to push", name)
	}
	client, err := secretClient(server)
	if err != nil {
		return err
	}
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	src := &secretSources{dataDir: remoteDataDir(), repoRoot: initRepoRoot("")}
	var failed []error
	for _, d := range def.Secret {
		value, err := src.value(ctx, d)
		if err != nil {
			failed = append(failed, secretFailure(d.Key, err))
			continue
		}
		exp, err := putDeclared(ctx, client, d, value, time.Now())
		if err == nil {
			if gerr := client.GrantSecret(ctx, d.Scope, d.Workspace, d.Key, remoteID); gerr != nil {
				err = secretFailure(d.Key, gerr)
			}
		}
		if err != nil {
			failed = append(failed, err)
			continue
		}
		fmt.Fprintf(out, "pushed %s (%s), granted to %s%s\n", d.Key, d.Scope, remoteID, expiryNote(exp))
	}
	for _, e := range failed {
		fmt.Fprintln(cmd.ErrOrStderr(), e)
	}
	if len(failed) > 0 {
		return fmt.Errorf("remote %s: %d of %d secret(s) not pushed", name, len(failed), len(def.Secret))
	}
	return nil
}

// refreshTTLSecrets re-obtains and pushes again each declared secret with a ttl
// whose copy on satelle.dev is missing, lapsed or within a tenth of its ttl of
// lapsing, so a run never starts on a value about to expire. satelle.dev's own
// expires_at is the only record of when a copy lapses: no local state is kept. It
// does not grant again (the grant stands) and prints the key, never the value.
func refreshTTLSecrets(ctx context.Context, out io.Writer, c *hosted.Client, def remotedef.Definition, now time.Time) error {
	type scopeAt struct{ scope, workspace string }
	held := map[scopeAt]map[string]hosted.SecretMeta{}
	src := &secretSources{dataDir: remoteDataDir(), repoRoot: initRepoRoot("")}
	for _, d := range def.Secret {
		ttl, ok := d.TTLDuration()
		if !ok {
			continue
		}
		s := scopeAt{d.Scope, d.Workspace}
		if held[s] == nil {
			metas, err := c.ListSecrets(ctx, d.Scope, d.Workspace)
			if err != nil {
				return fmt.Errorf("list %s secrets: %w", d.Scope, err)
			}
			held[s] = map[string]hosted.SecretMeta{}
			for _, m := range metas {
				held[s][m.Key] = m
			}
		}
		if m, have := held[s][d.Key]; have && m.HasValue {
			if exp, hasExp := m.Expiry(); hasExp && exp.Sub(now) > ttl/10 {
				continue
			}
		}
		value, err := src.value(ctx, d)
		if err != nil {
			return secretFailure(d.Key, err)
		}
		exp, err := putDeclared(ctx, c, d, value, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "refreshed %s (%s)%s\n", d.Key, d.Scope, expiryNote(exp))
	}
	return nil
}

func expiryNote(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return ", expires " + t.UTC().Format(time.RFC3339)
}

func runSecretList(cmd *cobra.Command, name, workspace, server string) error {
	type scopeAt struct{ scope, workspace string }
	scopes := []scopeAt{{hosted.ScopePersonal, ""}}
	if name != "" {
		def, err := loadRemoteDef(name)
		if err != nil {
			return err
		}
		scopes = nil
		seen := map[scopeAt]bool{}
		for _, d := range def.Secret {
			if s := (scopeAt{d.Scope, d.Workspace}); !seen[s] {
				seen[s] = true
				scopes = append(scopes, s)
			}
		}
		if len(scopes) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "remote %s declares no [[secret]]\n", name)
			return nil
		}
	}
	if workspace != "" {
		scopes = append(scopes, scopeAt{hosted.ScopeTeam, workspace})
	}
	client, err := secretClient(server)
	if err != nil {
		return err
	}
	var all []hosted.SecretMeta
	for _, s := range scopes {
		metas, err := client.ListSecrets(cmd.Context(), s.scope, s.workspace)
		if err != nil {
			return fmt.Errorf("list %s secrets: %w", s.scope, err)
		}
		all = append(all, metas...)
	}
	printSecrets(cmd.OutOrStdout(), all)
	return nil
}

// printSecrets renders the server's listing: a key, its scope, who may read it and
// when it lapses. There is no value to show.
func printSecrets(out io.Writer, metas []hosted.SecretMeta) {
	if len(metas) == 0 {
		fmt.Fprintln(out, "no secrets on satelle.dev — satelle secret push <remote> sends a remote's declared secrets")
		return
	}
	sort.SliceStable(metas, func(i, j int) bool { return metas[i].Scope+metas[i].Key < metas[j].Scope+metas[j].Key })
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSCOPE\tREADERS\tEXPIRES")
	for _, m := range metas {
		readers := "-"
		if len(m.Readers) > 0 {
			readers = strings.Join(m.Readers, ",")
		}
		expires := "-"
		if m.ExpiresAt != "" {
			expires = m.ExpiresAt
			if m.Expired {
				expires += " (expired)"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Key, m.Scope, readers, expires)
	}
	_ = tw.Flush()
}
