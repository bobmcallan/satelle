// `satelle remote run <name>` is the remote integration harness: it starts a
// fresh node from the named definition, offers each selected story to it through
// remotedef.Wake (so the definition's own contract decides what runs), brings
// each story's hosted state home with a scoped sync pull, and reports what came
// back — the status the story was left at, the hand-back result, the reviewers'
// verdicts, where the node logged the story's sessions, any change to the node's
// own configuration, and the spend against the definition's cap. The command
// names no status, workflow or repo; all of that is the definition's.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// workstateSync is how the harness moves story state between the machines. It
// only ever pulls: the node reaches the story's hosted record through the
// definition's own story command, and nothing local is pushed onto it. Push is
// part of the interface so a test can prove it is never called.
type workstateSync interface {
	Pull(ctx context.Context, cmd *cobra.Command, ids []string) error
	Push(ctx context.Context, cmd *cobra.Command, ids []string) error
}

type realWorkstateSync struct{}

func (realWorkstateSync) Pull(_ context.Context, cmd *cobra.Command, ids []string) error {
	return runSyncWorkstatePull(cmd, "", false, false, false, ids...)
}

func (realWorkstateSync) Push(_ context.Context, cmd *cobra.Command, ids []string) error {
	return runSyncWorkstatePush(cmd, "", false, false, ids...)
}

// remoteRunSync is the sync a run uses; a test replaces it.
var remoteRunSync workstateSync = realWorkstateSync{}

// storyReporter is the started node as the harness asks about a story.
type storyReporter interface {
	StoryReport(ctx context.Context, story string, since int64) (remotenode.StoryReportReply, error)
}

func newRemoteRunCmd() *cobra.Command {
	var server, record, tag string
	var ids []string
	run := &cobra.Command{
		Use:   "run <name> (--story <id>... | --tag <tag>) --story-of-record <id>",
		Short: "Start a fresh node from a remote definition, run its workflow on selected stories, and report",
		Long: `Starts a node fresh (as "remote start" does), then offers each selected story
(--story ids, or every story carrying --tag) through the remote's own contract;
one outside it is reported refused and not run. Each story is pulled back from
the hosted project, never pushed, and checked against the contract's exits and
the local route.

Per story it prints, and records in one remote_run row on --story-of-record: the
status it was left at, the hand-back result, the reviewers' verdicts, where the
node logged its sessions, and "gap: start configuration changed" if the node's
own substrate changed during the story.

spend_cap_usd in remote.toml stops the run before the next story once the pulled
usage reaches it. The node is stopped at the end unless the lifecycle is "idle".
Needs satelle login.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemoteRun(cmd, args[0], server, record, ids, tag)
		},
	}
	run.Flags().StringSliceVar(&ids, "story", nil, "a story to run (repeatable); excludes --tag")
	run.Flags().StringVar(&tag, "tag", "", "run every story carrying this tag; excludes --story")
	run.Flags().StringVar(&record, "story-of-record", "", "the story the run's remote_preflight and remote_run rows are recorded on (required)")
	_ = run.MarkFlagRequired("story-of-record")
	run.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server)")
	return run
}

func runRemoteRun(cmd *cobra.Command, name, serverFlag, record string, ids []string, tag string) (err error) {
	switch {
	case len(ids) > 0 && tag != "":
		return errors.New("--story and --tag are alternatives: select stories by one or the other")
	case len(ids) == 0 && tag == "":
		return errors.New("name the stories to run with --story <id>... or --tag <tag>")
	}
	def, err := loadRemoteDef(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(def.Host.StoryCommand) == "" {
		return fmt.Errorf("remote %s declares no host.story_command: there is nothing to run on a story", name)
	}
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	items := a.Store.Stories
	if ids, err = selectRunStories(ctx, items, ids, tag); err != nil {
		return err
	}
	if _, err := items.Get(ctx, record); err != nil {
		return fmt.Errorf("--story-of-record %s: %w", record, err)
	}

	s, err := startRemoteRun(cmd, name, serverFlag, record)
	if err != nil {
		return err
	}
	defer s.finish(&err)

	started := time.Now()
	plan := remoteRunPlan{
		Name: name, RunID: newRunID(), Def: &s.def, Record: record, IDs: ids,
		Baseline: s.node.Report, Started: started, Node: s.node,
		Stories:  storyReader{items},
		Recorder: ledgerRecorder{a.Store.Ledger},
		Runner:   remotenode.StoryRunner{Commands: &remotenode.Commands{Def: s.def, Env: s.env, Log: s.hostLog}},
		Local:    localRoute{items, a.Store.DocIndex},
		Pull:     func(ctx context.Context, ids []string) error { return remoteRunSync.Pull(ctx, cmd, ids) },
		Items:    items, Ledger: a.Store.Ledger, Out: s.out,
	}
	summary, runErr := runRemoteStories(s.ctx, plan)
	if rerr := recordRemoteRun(s.ctx, a.Store.Ledger, record, summary); rerr != nil {
		runErr = errors.Join(runErr, rerr)
	}
	return runErr
}

// selectRunStories is the run's story ids, in order and without repeats: the ids
// given, each of which must exist, or the stories carrying tag.
func selectRunStories(ctx context.Context, items *workitem.Store, ids []string, tag string) ([]string, error) {
	if tag != "" {
		tagged, err := items.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Tag: tag, Limit: 2000})
		if err != nil {
			return nil, err
		}
		for _, it := range tagged {
			ids = append(ids, it.ID)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("no story carries the tag %q", tag)
		}
		sort.Strings(ids)
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := items.Get(ctx, id); err != nil {
			return nil, fmt.Errorf("story %s: %w", id, err)
		}
		out = append(out, id)
	}
	return out, nil
}

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run_%x", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(b)
}

// remoteRunPlan is everything runRemoteStories needs: the definition, the
// selected stories, the started node's join report (the baseline its clock and
// configuration hashes are measured from), and the seams to the stories, the
// ledger, the host and the sync.
type remoteRunPlan struct {
	Name, RunID, Record string
	Def                 *remotedef.Definition
	IDs                 []string
	Baseline            remotenode.Report
	Started             time.Time
	Node                storyReporter

	Stories  remotedef.StoryReader
	Recorder remotedef.HandoffRecorder
	Runner   remotedef.Runner
	Local    remotedef.LocalRoute
	// Pull brings the named stories' hosted state to this machine.
	Pull func(ctx context.Context, ids []string) error

	Items  *workitem.Store
	Ledger *ledger.Store
	Out    io.Writer
}

// runVerdict is one reviewer verdict row, as the pulled ledger holds it.
type runVerdict struct {
	Kind  string `json:"kind"`
	Skill string `json:"skill,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

// runStory is what the run found for one selected story.
type runStory struct {
	ID         string       `json:"id"`
	Status     string       `json:"status,omitempty"`
	Refused    string       `json:"refused,omitempty"`
	Skipped    string       `json:"skipped,omitempty"`
	Error      string       `json:"error,omitempty"`
	InContract bool         `json:"in_contract"`
	LocalKnown bool         `json:"local_known"`
	Reason     string       `json:"reason,omitempty"`
	Verdicts   []runVerdict `json:"verdicts"`
	Sessions   []string     `json:"sessions"`
	Gap        []string     `json:"gap"`
}

// remoteRunSummary is the payload of a remote_run row.
type remoteRunSummary struct {
	Run      string     `json:"run"`
	Remote   string     `json:"remote"`
	Started  time.Time  `json:"started"`
	Ended    time.Time  `json:"ended"`
	Stories  []runStory `json:"stories"`
	SpendUSD float64    `json:"spend_usd"`
	// CapUSD is nil for an uncapped run.
	CapUSD *float64 `json:"cap_usd"`
	Capped bool     `json:"capped"`
	Error  string   `json:"error,omitempty"`
}

// runRemoteStories works the plan's stories in order. A refused story is
// reported and the run goes on; a run, pull or report error ends it, and so does
// the spend cap, which is checked before each story. It returns what it found
// (always, even with an error) and the error that ended the run.
func runRemoteStories(ctx context.Context, p remoteRunPlan) (remoteRunSummary, error) {
	sum := remoteRunSummary{Run: p.RunID, Remote: p.Name, Started: p.Started, CapUSD: p.Def.SpendCapUSD, Stories: []runStory{}}
	// The node wrote the ledger rows the pull brings home, so the window opens at
	// the node's clock when it joined, not this machine's.
	window := p.Started
	if p.Baseline.Now > 0 {
		window = time.Unix(0, p.Baseline.Now)
	}
	since, prev := p.Baseline.Now, p.Baseline.Hashes
	wiring := remotedef.Wiring{
		Stories: p.Stories, Recorder: p.Recorder, Runner: p.Runner, Local: p.Local,
		AfterRun: func(ctx context.Context, id string) error { return p.Pull(ctx, []string{id}) },
	}

	var runErr error
	var ran []string
	for i, id := range p.IDs {
		if capReached(sum.CapUSD, sum.SpendUSD) {
			sum.Capped = true
			fmt.Fprintf(p.Out, "cap reached: $%.4f of $%.2f; %d story(ies) not run\n", sum.SpendUSD, *sum.CapUSD, len(p.IDs)-i)
			for _, rest := range p.IDs[i:] {
				sum.Stories = append(sum.Stories, runStory{ID: rest, Skipped: "spend cap reached", Verdicts: []runVerdict{}, Sessions: []string{}, Gap: []string{}})
			}
			break
		}
		st := runStory{ID: id, Verdicts: []runVerdict{}, Sessions: []string{}, Gap: []string{}}
		runErr = p.runOne(ctx, wiring, &st, &since, &prev, window)
		sum.Stories = append(sum.Stories, st)
		if st.Refused != "" {
			continue // nothing ran: no spend, nothing to read back
		}
		ran = append(ran, id)
		if spend, unpriced, serr := p.spend(ctx, ran, window); serr != nil {
			runErr = errors.Join(runErr, serr)
		} else {
			sum.SpendUSD = spend
			printSpend(p.Out, spend, unpriced, sum.CapUSD)
		}
		if runErr != nil {
			break
		}
	}
	sum.Ended = time.Now()
	if runErr != nil {
		sum.Error = runErr.Error()
	}
	return sum, runErr
}

func capReached(limit *float64, spent float64) bool { return limit != nil && spent >= *limit }

// runOne offers one story to the remote and reads back what it left, filling st.
// since and prev advance to this story's node clock and configuration hashes.
func (p remoteRunPlan) runOne(ctx context.Context, w remotedef.Wiring, st *runStory, since *int64, prev *map[string]string, window time.Time) error {
	_, backs, err := remotedef.Wake(ctx, p.Name, p.Def, remotedef.WakeRequest{TriggerID: p.RunID, Remote: p.Name, StoryID: st.ID}, w)
	var refused *remotedef.RefusedError
	if errors.As(err, &refused) {
		st.Refused = refused.Cause.Error()
		fmt.Fprintf(p.Out, "story %s: refused: %s\n", st.ID, st.Refused)
		return nil
	}
	if err != nil {
		st.Error = err.Error()
		fmt.Fprintf(p.Out, "story %s: failed: %v\n", st.ID, err)
		return err
	}

	// The story was pulled before the hand-back check; read it the same way.
	ref, err := p.Stories.Story(ctx, st.ID)
	if err != nil {
		st.Error = err.Error()
		return err
	}
	st.Status, st.InContract, st.LocalKnown = ref.Status, true, true
	if len(backs) > 0 {
		st.InContract, st.LocalKnown, st.Reason = backs[0].InContract, backs[0].LocalKnown, backs[0].Reason
	}
	handback := "hand-back ok (in the contract's exits and the local route)"
	if st.Reason != "" {
		handback = "hand-back: " + st.Reason
	}
	fmt.Fprintf(p.Out, "story %s: left at %s; %s\n", st.ID, st.Status, handback)

	if st.Verdicts, err = p.verdicts(ctx, st.ID, window); err != nil {
		st.Error = err.Error()
		return err
	}
	for _, v := range st.Verdicts {
		fmt.Fprintf(p.Out, "story %s: verdict %s %s (%s→%s)\n", st.ID, v.Kind, v.Skill, v.From, v.To)
	}

	rep, err := p.Node.StoryReport(ctx, st.ID, *since)
	if err != nil {
		st.Error = fmt.Sprintf("story report: %v", err)
		fmt.Fprintf(p.Out, "story %s: failed: %s\n", st.ID, st.Error)
		return fmt.Errorf("story %s: %s", st.ID, st.Error)
	}
	if rep.Sessions != nil {
		st.Sessions = rep.Sessions
	}
	if len(st.Sessions) == 0 {
		fmt.Fprintf(p.Out, "session %s: none logged on the node\n", st.ID)
	}
	for _, s := range st.Sessions {
		fmt.Fprintf(p.Out, "session %s: %s\n", st.ID, s)
	}
	for _, f := range config.DiffTreeHashes(*prev, rep.Hashes) {
		st.Gap = append(st.Gap, f.Path)
	}
	if len(st.Gap) > 0 {
		fmt.Fprintf(p.Out, "gap: start configuration changed during %s: %s\n", st.ID, strings.Join(st.Gap, ", "))
	}
	*since, *prev = rep.Now, rep.Hashes
	return nil
}

// verdicts is the story's review_accept and review_reject rows written at or
// after window, oldest first.
func (p remoteRunPlan) verdicts(ctx context.Context, id string, window time.Time) ([]runVerdict, error) {
	rows, err := p.Ledger.ListByStory(ctx, id, "")
	if err != nil {
		return nil, err
	}
	out := []runVerdict{}
	for _, e := range rows {
		if (e.Kind != ledger.KindReviewAccept && e.Kind != ledger.KindReviewReject) || e.CreatedAt.Before(window) {
			continue
		}
		v := runVerdict{Kind: e.Kind}
		_ = json.Unmarshal(e.Payload, &v)
		v.Kind = e.Kind
		out = append(out, v)
	}
	return out, nil
}

// spend is what the run's stories cost since window: the dollars the pulled
// ledger's usage rows reported, and how many rows reported none. costview owns
// the figures.
func (p remoteRunPlan) spend(ctx context.Context, ids []string, window time.Time) (usd float64, unpriced int, err error) {
	for _, id := range ids {
		item, err := p.Items.Get(ctx, id)
		if err != nil {
			return 0, 0, err
		}
		rows, err := p.Ledger.ListByStory(ctx, id, "")
		if err != nil {
			return 0, 0, err
		}
		var since []ledger.Entry
		for _, e := range rows {
			if !e.CreatedAt.Before(window) {
				since = append(since, e)
			}
		}
		f := costview.Own(item, since, costview.Clock{}, time.Now()).Figures
		usd += f.CostUSD
		unpriced += f.CostUnavailableRows
	}
	return usd, unpriced, nil
}

func printSpend(out io.Writer, usd float64, unpriced int, limit *float64) {
	against := "uncapped"
	if limit != nil {
		against = fmt.Sprintf("of $%.2f cap", *limit)
	}
	note := ""
	if unpriced > 0 {
		note = fmt.Sprintf(" (%d usage row(s) reported no cost)", unpriced)
	}
	fmt.Fprintf(out, "spend $%.4f %s%s\n", usd, against, note)
}

// recordRemoteRun writes the run's one remote_run row on the story of record.
func recordRemoteRun(ctx context.Context, l *ledger.Store, record string, sum remoteRunSummary) error {
	payload, err := json.Marshal(sum)
	if err != nil {
		return err
	}
	_, err = l.Append(ctx, ledger.AppendInput{
		StoryID: record, Kind: ledger.KindRemoteRun, Actor: "remote",
		Body:    fmt.Sprintf("remote %s run %s: %d story(ies), spend $%.4f", sum.Remote, sum.Run, len(sum.Stories), sum.SpendUSD),
		Payload: payload,
	}, sum.Ended)
	return err
}
