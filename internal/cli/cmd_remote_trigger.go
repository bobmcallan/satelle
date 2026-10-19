// `satelle remote trigger …` registers, lists, fires, pauses and resumes a
// remote's declared triggers through satelle-server, and `satelle remote wake`
// handles the WakeRequest the server delivers when one fires. The server runs
// every trigger; this side declares them and records the hand-off.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// remoteIDHelp explains the first argument every trigger command takes. The
// routes are keyed by it and the server checks project membership on it, so no
// project is ever sent.
const remoteIDHelp = `<remote-id> is the remote's id on satelle-server, from its registration record
there. The hosted server checks your membership on that id; no project is sent.`

func newRemoteTriggerCmd() *cobra.Command {
	var server, def string
	trigger := &cobra.Command{
		Use:   "trigger",
		Short: "Register, list, fire, pause and resume a remote's declared triggers on the server",
		Long: `A remote's triggers are the [[trigger]] entries of its definition. The server
fires them (a schedule, a story event, a hand-off, an inbound event, an MCP
call) and wakes the remote; these commands manage them through the server.

` + remoteIDHelp,
	}
	trigger.PersistentFlags().StringVar(&server, "server", "", "hosted server URL (default: the one you signed in to)")

	client := func(cmd *cobra.Command) (*hosted.Client, error) { return remoteClient(server) }
	// checkDeclared holds a trigger name to the local definition named by --def.
	checkDeclared := func(name string) error {
		if def == "" || name == "" {
			return nil
		}
		d, err := loadRemoteDef(def)
		if err != nil {
			return err
		}
		for _, t := range d.Triggers {
			if t.Name == name {
				return nil
			}
		}
		return fmt.Errorf("remote %s declares no trigger %q", def, name)
	}

	register := &cobra.Command{
		Use:   "register <remote-id> --def <name>",
		Short: "Replace the server's declared triggers with the definition's [[trigger]] set",
		Long: `Sends the name, kind and params of every [[trigger]] in the named remote
definition, replacing the server's declared set. Params go exactly as written
(none are sent for a trigger without any) and are not checked here: the server
judges them and a 400 is reported with its reason. Triggers kept by name keep
their paused state; dropped ones go. Refuses a definition that does not validate.

` + remoteIDHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if def == "" {
				return fmt.Errorf("--def <name> is required: the remote definition whose triggers to register")
			}
			if err := reportRemote(cmd, def, remotedef.Dir(remoteDataDir(), def)); err != nil {
				return err
			}
			d, err := loadRemoteDef(def)
			if err != nil {
				return err
			}
			declared := make([]hosted.DeclaredTrigger, 0, len(d.Triggers))
			for _, t := range d.Triggers {
				declared = append(declared, hosted.DeclaredTrigger{Name: t.Name, Kind: t.Kind, Params: t.Params})
			}
			c, err := hostedClient(server)
			if err != nil {
				return err
			}
			if err := c.PutDeclaredTriggers(cmd.Context(), args[0], declared); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "registered %d trigger(s) for remote %s\n", len(declared), args[0])
			return nil
		},
	}

	list := &cobra.Command{
		Use:   "list <remote-id>",
		Short: "List the remote's declared triggers with their paused state",
		Long:  remoteIDHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := hostedClient(server)
			if err != nil {
				return err
			}
			ts, err := c.ListDeclaredTriggers(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tKIND\tPAUSED\tPARAMS\tNEXT FIRE\tUPDATED")
			for _, t := range ts {
				fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\t%s\n", t.Name, t.Kind, t.Paused, paramsOrDash(t.Params), dashIfEmpty(t.NextFireAt), t.UpdatedAt)
			}
			return w.Flush()
		},
	}

	fires := &cobra.Command{
		Use:   "fires <remote-id>",
		Short: "List the fires the server recorded for the remote, with their source and outcome",
		Long: `Lists the server's fire records for the remote, refused ones included. SOURCE is
the surface the fire came through, shown exactly as the server sends it.

` + remoteIDHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(cmd)
			if err != nil {
				return err
			}
			recs, err := c.ListFireRecords(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "FIRE\tTRIGGER\tSOURCE\tOUTCOME")
			for _, r := range recs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.FireID(), triggerOrNone(r.Trigger), dashIfEmpty(r.Source), dashIfEmpty(r.Outcome))
			}
			return w.Flush()
		},
	}

	var story string
	fire := &cobra.Command{
		Use:   "fire <remote-id> [<trigger>] [--story <id>]",
		Short: "Fire the remote, optionally naming a declared trigger and a story",
		Long: `Asks the server to fire the remote. A trigger names one of its declared
triggers; a story names the story to run (none starts the remote's sweep of its
entry statuses). An undeclared or paused trigger is refused with the server's
reason. A wake the server could not deliver is reported as wake failed; the fire
is still recorded. With --def, the trigger name is first checked against that definition.

` + remoteIDHelp,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 2 {
				name = args[1]
			}
			if err := checkDeclared(name); err != nil {
				return err
			}
			c, err := hostedClient(server)
			if err != nil {
				return err
			}
			res, err := c.FireRemote(cmd.Context(), args[0], story, name)
			if err != nil {
				return err
			}
			printFired(cmd.OutOrStdout(), args[0], res)
			return nil
		},
	}
	fire.Flags().StringVar(&story, "story", "", "the story to run (none starts the remote's sweep)")

	setPaused := func(use, short string, paused bool) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <remote-id> <trigger>",
			Short: short,
			Long:  remoteIDHelp,
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := checkDeclared(args[1]); err != nil {
					return err
				}
				c, err := hostedClient(server)
				if err != nil {
					return err
				}
				if err := c.SetTriggerPaused(cmd.Context(), args[0], args[1], paused); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%sd trigger %s on remote %s\n", use, args[1], args[0])
				return nil
			},
		}
	}

	trigger.PersistentFlags().StringVar(&def, "def", "", "the remote definition (by name) to read triggers from or check a name against")
	trigger.AddCommand(register, list, fires, fire,
		setPaused("pause", "Pause a declared trigger: a fire naming it is refused", true),
		setPaused("resume", "Resume a paused trigger", false))
	return trigger
}

// remoteClient is the hosted client for the server flag (else the server signed
// in to), shared by every command that talks to satelle-server about a remote.
func remoteClient(server string) (*hosted.Client, error) {
	s := resolveServer(server)
	if s == "" {
		return nil, fmt.Errorf("%w — run \"satelle login\" first (no hosted server configured)", hosted.ErrLoginRequired)
	}
	return hosted.NewClient(s, hosted.DefaultStore(), nil), nil
}

func newRemoteWakeCmd() *cobra.Command {
	var request string
	wake := &cobra.Command{
		Use:   "wake <name> [--request -]",
		Short: "Handle a WakeRequest the server delivered: hand off the stories it selects",
		Long: `Reads one WakeRequest (JSON) from stdin, or from the file --request names, and
hands off to the named remote the story it names, or with no story every story
its segments accept by entry status and category. Each hand-off is a
remote_handoff ledger entry carrying the fire's source and any event's fields
as sent; an event never chooses the story. With host.story_command declared,
each new hand-off runs it through host.run.

Refused, with nothing recorded: an undeclared trigger, a story outside the
contract, and a request with no fire-record id. A redelivered request records
nothing new. A story left outside the contract's exits or the local route is
recorded as remote_handback.

The caller has already checked the wake credential.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := loadRemoteDef(args[0])
			if err != nil {
				return err
			}
			raw, err := readRequest(cmd, request)
			if err != nil {
				return err
			}
			var req remotedef.WakeRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return fmt.Errorf("wake request is not valid JSON: %w", err)
			}
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			wiring := remotedef.Wiring{
				Stories:  storyReader{a.Store.Stories},
				Recorder: ledgerRecorder{a.Store.Ledger},
				Local:    localRoute{a.Store.Stories, a.Store.DocIndex},
			}
			// A definition without a story command wires no Runner: its wake
			// records the hand-offs and runs nothing, so no story is re-read for
			// a hand-back.
			if strings.TrimSpace(def.Host.StoryCommand) != "" {
				env, err := hostEnv(a, def)
				if err != nil {
					return fmt.Errorf("remote %s: host.env: %w", args[0], err)
				}
				wiring.Runner = remotenode.StoryRunner{Commands: &remotenode.Commands{Def: def, Env: env, Log: cmd.ErrOrStderr()}}
			}
			handoffs, handbacks, err := remotedef.Wake(cmd.Context(), args[0], &def, req, wiring)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(handoffs) == 0 {
				fmt.Fprintln(out, "no new hand-off")
			}
			for _, h := range handoffs {
				at := ""
				if h.Branch != "" {
					at = " at " + h.Branch + "@" + h.SHA
				}
				fmt.Fprintf(out, "handed off %s to remote %s (trigger %s, fire %s)%s\n", h.Story, h.Remote, triggerOrNone(h.Trigger), h.FireID, at)
			}
			printHandbacks(out, handbacks)
			return nil
		},
	}
	wake.Flags().StringVar(&request, "request", "-", "where the WakeRequest JSON is read from: - for stdin, or a file")
	return wake
}

// printFired reports a fire the server accepted, for `trigger fire` and `run`.
func printFired(w io.Writer, remoteID string, res hosted.FireResult) {
	fmt.Fprintf(w, "fired remote %s (fire record %s)\n", remoteID, res.TriggerID)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// paramsOrDash renders a trigger's params as k=v pairs sorted by key.
func paramsOrDash(p map[string]string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + p[k]
	}
	return dashIfEmpty(strings.Join(pairs, ","))
}

func triggerOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func readRequest(cmd *cobra.Command, from string) ([]byte, error) {
	if from == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(from)
}

// loadRemoteDef reads the named remote's definition from this repo.
func loadRemoteDef(name string) (remotedef.Definition, error) {
	if !remotedef.ValidName(name) {
		return remotedef.Definition{}, fmt.Errorf("remote name %q must be lower-case letters, digits, - or _, starting with a letter or digit", name)
	}
	dir := remotedef.Dir(remoteDataDir(), name)
	if _, err := os.Stat(dir); err != nil {
		return remotedef.Definition{}, fmt.Errorf("no remote %q under %s/%s", name, config.DefaultDataDir, config.RemotesDir)
	}
	return remotedef.Load(dir)
}

// printHandbacks reports each story a remote left outside its contract or its
// local route; a story that passed both is not mentioned.
func printHandbacks(w io.Writer, handbacks []remotedef.Handback) {
	for _, b := range handbacks {
		fmt.Fprintf(w, "remote %s: %s\n", b.Remote, b.Report())
	}
}

// storyReader adapts the story store to what a wake selects from.
type storyReader struct{ s *workitem.Store }

func (r storyReader) Story(ctx context.Context, id string) (remotedef.StoryRef, error) {
	it, err := r.s.Get(ctx, id)
	if err != nil {
		return remotedef.StoryRef{}, err
	}
	return remotedef.StoryRef{ID: it.ID, Status: it.Status, Category: it.Category}, nil
}

func (r storyReader) At(ctx context.Context, status string) ([]remotedef.StoryRef, error) {
	items, err := r.s.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Status: status, Limit: 2000})
	if err != nil {
		return nil, err
	}
	refs := make([]remotedef.StoryRef, 0, len(items))
	for _, it := range items {
		refs = append(refs, remotedef.StoryRef{ID: it.ID, Status: it.Status, Category: it.Category})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return refs, nil
}

// localRoute answers whether the local workflow governing a story declares a
// status, by the same resolution every transition gate uses (wfgovern.SpecFor).
type localRoute struct {
	s    *workitem.Store
	docs *docindex.Store
}

// spec is the workflow spec governing story id, with the route's name.
func (r localRoute) spec(ctx context.Context, id string) (wfdot.Spec, string, error) {
	it, err := r.s.Get(ctx, id)
	if err != nil {
		return wfdot.Spec{}, "", err
	}
	wfs, err := r.docs.List(ctx, "workflows")
	if err != nil {
		return wfdot.Spec{}, "", err
	}
	spec, route, _, err := wfgovern.SpecFor(wfs, it)
	if err != nil {
		return wfdot.Spec{}, route, fmt.Errorf("story %s: local workflow does not resolve: %w", id, err)
	}
	return spec, route, nil
}

// Engages reports whether status is one of the route's non-terminal engaging
// states, the classification the engagement seat is held by (anyEngaged).
func (r localRoute) Engages(ctx context.Context, id, status string) (bool, string, error) {
	spec, route, err := r.spec(ctx, id)
	if err != nil {
		return false, route, err
	}
	for _, s := range spec.NonTerminalEngagingStates() {
		if s == status {
			return true, route, nil
		}
	}
	return false, route, nil
}

func (r localRoute) Declares(ctx context.Context, id, status string) (bool, string, error) {
	spec, route, err := r.spec(ctx, id)
	if err != nil {
		return false, route, err
	}
	for _, s := range spec.States {
		if s.Name == status {
			return true, route, nil
		}
	}
	return false, route, nil
}

// ledgerRecorder records a hand-off as a remote_handoff ledger entry, once per
// fire and story.
type ledgerRecorder struct{ l *ledger.Store }

// RecordHandback records a story left outside the contract or the local route as
// a remote_handback ledger entry, once per fire, story and status.
func (r ledgerRecorder) RecordHandback(ctx context.Context, b remotedef.Handback) error {
	payload, err := json.Marshal(map[string]any{"remote": b.Remote, "status": b.Status, "in_contract": b.InContract, "local_known": b.LocalKnown, "holds_seat": b.HoldsSeat, "refused": b.Refused, "reason": b.Reason})
	if err != nil {
		return err
	}
	refs, err := json.Marshal(map[string]string{"fire_id": b.FireID, "story": b.Story, "status": b.Status})
	if err != nil {
		return err
	}
	body := fmt.Sprintf("remote %s left %s at %s: %s", b.Remote, b.Story, b.Status, b.Reason)
	if b.Refused {
		body = fmt.Sprintf("remote %s did not take %s at %s; %s", b.Remote, b.Story, b.Status, b.Reason)
	}
	_, _, err = r.l.AppendOnce(ctx, ledger.AppendInput{
		StoryID: b.Story, Kind: ledger.KindRemoteHandback, Actor: "remote",
		Body:    body,
		Payload: payload, Refs: refs,
	}, time.Now())
	return err
}

// RecordRun records one step run as a remote_run ledger entry, once per run id.
// Its payload is the fixed ledger.RemoteRunPayload: no env, argv or credential.
func (r ledgerRecorder) RecordRun(ctx context.Context, run remotedef.RunRecord) error {
	p := ledger.RemoteRunPayload{
		Remote: run.Remote, Version: run.Version, Host: run.Host, Label: run.Label,
		Step: run.Step, RunID: run.RunID,
		Started: run.Started.UTC().Format(time.RFC3339), Ended: run.Ended.UTC().Format(time.RFC3339),
		Error: run.Error,
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	refs, err := json.Marshal(map[string]string{"fire_id": run.FireID, "story": run.Story, "run_id": run.RunID})
	if err != nil {
		return err
	}
	_, _, err = r.l.AppendOnce(ctx, ledger.AppendInput{
		StoryID: run.Story, Kind: ledger.KindRemoteRun, Actor: "remote",
		Body:    fmt.Sprintf("remote %s ran %s of %s as %s (run %s)", run.Remote, run.Step, run.Story, run.Label, run.RunID),
		Payload: payload, Refs: refs,
	}, time.Now())
	return err
}

func (r ledgerRecorder) Record(ctx context.Context, h remotedef.Handoff) (bool, error) {
	fields := map[string]string{"remote": h.Remote, "trigger": h.Trigger, "kind": h.Kind, "fire_id": h.FireID}
	// The fire's source and event ride along as sent; a wake that carries none
	// records exactly the keys above.
	for k, v := range map[string]string{"source": h.Source, "event_kind": h.EventKind, "repo": h.Repo, "branch": h.Branch, "sha": h.SHA} {
		if v != "" {
			fields[k] = v
		}
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}
	refs, err := json.Marshal(map[string]string{"fire_id": h.FireID, "story": h.Story})
	if err != nil {
		return false, err
	}
	_, inserted, err := r.l.AppendOnce(ctx, ledger.AppendInput{
		StoryID: h.Story, Kind: ledger.KindRemoteHandoff, Actor: "remote",
		Body:    fmt.Sprintf("handed %s to remote %s (trigger %s, fire %s)", h.Story, h.Remote, triggerOrNone(h.Trigger), h.FireID),
		Payload: payload, Refs: refs,
	}, time.Now())
	return inserted, err
}
