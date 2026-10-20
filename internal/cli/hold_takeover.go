package cli

// Taking a story's hold from another location is a decision about two things at
// once (sty_6e5fc41c): the story's status and its code. The hold moves only when
// the holder is out of the way and this machine's copy of both agrees with the
// holder's. This file assesses that; cmd_story_hold.go acts on it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// takeoverFetchBudget bounds the fetch a takeover attempts before it looks for
// the heads the story's ledger claims.
const takeoverFetchBudget = 60 * time.Second

// takeoverActor is the ledger actor a forced takeover is recorded under.
const takeoverActor = "operator"

// takeoverCheck is one of the three things a takeover needs, failed.
type takeoverCheck struct {
	Name    string `json:"name"`
	Failure string `json:"failure"`
	Fix     string `json:"fix"`
}

func (c takeoverCheck) String() string {
	return fmt.Sprintf("%s: %s — %s", c.Name, c.Failure, c.Fix)
}

// assessTakeover runs the three takeover checks for id and returns the ones that
// failed, none when the hold may move. The status check goes first: its scoped
// snapshot brings in the hosted copy and the ledger rows the holder wrote, which
// the liveness and code checks read. A check that cannot be made fails; it is
// never skipped.
func assessTakeover(ctx context.Context, a *app.App, errw io.Writer, client *hosted.Client, server, project, id string, now time.Time) ([]takeoverCheck, error) {
	var failed []takeoverCheck
	hostedTerminal, statusFail := assessTakeoverStatus(ctx, a, errw, client, server, project, id)
	if statusFail != nil {
		failed = append(failed, *statusFail)
	}
	liveFail, err := assessTakeoverLiveness(ctx, a, client, project, id, hostedTerminal, now)
	if err != nil {
		return nil, err
	}
	if liveFail != nil {
		failed = append(failed, *liveFail)
	}
	if codeFail := assessTakeoverCode(ctx, a, id); codeFail != nil {
		failed = append(failed, *codeFail)
	}
	return failed, nil
}

// assessTakeoverStatus is the status check: a scoped snapshot of the story
// records no conflict for it. It also reports whether the hosted copy is at a
// terminal state of its route, which the liveness check accepts as the holder
// being finished.
func assessTakeoverStatus(ctx context.Context, a *app.App, errw io.Writer, client *hosted.Client, server, project, id string) (hostedTerminal bool, fail *takeoverCheck) {
	const name = "status"
	fix := fmt.Sprintf("satelle sync workstate pull %s (--force lets the hosted copy win)", id)
	optIn, err := workstateOptIn(a.Config)
	if err != nil {
		return false, &takeoverCheck{name, "could not read the [sync] areas: " + err.Error(), "fix satelle.toml"}
	}
	if !optIn["stories"] {
		return false, &takeoverCheck{name, "stories are not synced ([sync] stories is local), so the two copies cannot be compared", "set [sync] stories to personal or shared"}
	}
	sc, err := resolveScope(ctx, a, []string{id}, false)
	if err != nil {
		return false, &takeoverCheck{name, "could not scope the story: " + err.Error(), fix}
	}
	rep, perr := runScopedWorkstateSnapshot(ctx, a, errw, client, server, project, optIn, false, snapshotModeSnapshot, sc)
	recordStoryConflicts(ctx, a, client, project, rep)
	if perr != nil {
		return false, &takeoverCheck{name, "could not compare local and hosted: " + perr.Error(), "check the connection and `satelle login`, then " + fix}
	}
	var hostedStatus string
	for _, hi := range rep.Hosted {
		if hi.ID != id {
			continue
		}
		hostedStatus = hi.Status
		if it, perr := parseWorkstateItem(hi); perr == nil {
			rest := verb.StoryRouteRest(ctx, it)
			hostedTerminal = rest.Known && rest.Terminal
		}
	}
	if reason, ok := rep.Stories.Conflicts[id]; ok {
		localStatus := "unknown"
		if it, gerr := a.Store.Stories.Get(ctx, id); gerr == nil {
			localStatus = it.Status
		}
		return hostedTerminal, &takeoverCheck{name, fmt.Sprintf("local and hosted copies differ (%s): local status %s, hosted status %s", reason, localStatus, hostedStatus), fix}
	}
	return hostedTerminal, nil
}

// assessTakeoverLiveness is the first leg: the holder released the hold, the
// story is terminal on the hosted copy, or the holder's last-seen is older than
// [sync] hold_stale_after. A hold already held here needs no takeover assessed.
func assessTakeoverLiveness(ctx context.Context, a *app.App, client *hosted.Client, project, id string, hostedTerminal bool, now time.Time) (*takeoverCheck, error) {
	st, err := client.ItemHold(ctx, project, id)
	if err != nil && !errors.Is(err, hosted.ErrItemNotFound) {
		return nil, err
	}
	if st.LocationID == "" || st.LocationID == client.Location() || hostedTerminal {
		return nil, nil
	}
	staleAfter, err := config.HoldStaleAfter(a.Config)
	if err != nil {
		return nil, err
	}
	if dead, _ := hosted.HoldPresumedDead(st.LastSeenAt, staleAfter, now); dead {
		return nil, nil
	}
	who := st.LocationID
	if st.Label != "" {
		who = st.Label + " (" + st.LocationID + ")"
	}
	seen := hosted.FormatLastSeen(st.LastSeenAt)
	if seen == "" {
		seen = "never"
	}
	return &takeoverCheck{
		Name:    "liveness",
		Failure: fmt.Sprintf("holder %s is live: last seen %s", who, seen),
		Fix:     fmt.Sprintf("ask it to run `satelle story hold release %s`, or wait until it has been silent for %s ([sync] hold_stale_after)", id, staleAfter),
	}, nil
}

// assessTakeoverCode is the code check: after a fetch attempt, every head the
// story's synced ledger claims is reachable in this repository, and a story whose
// work started on the holder has a post-work head here. A fetch that fails
// matters only when it leaves a head missing.
func assessTakeoverCode(ctx context.Context, a *app.App, id string) *takeoverCheck {
	const name = "code"
	it, err := a.Store.Stories.Get(ctx, id)
	if err != nil {
		return &takeoverCheck{name, "the story is not in the local store: " + err.Error(), fmt.Sprintf("satelle sync workstate pull %s", id)}
	}
	ws, err := verb.StoryWorkState(ctx, it)
	if err != nil {
		return &takeoverCheck{name, "could not read the story's ledger: " + err.Error(), "retry"}
	}
	if ws.Unavailable != "" {
		return &takeoverCheck{name, "could not verify the code: " + ws.Unavailable, "resolve the story's workflow, then retry"}
	}
	heads := ws.ClaimedHeads()
	var fetchErr error
	if len(heads) > 0 {
		fctx, cancel := context.WithTimeout(ctx, takeoverFetchBudget)
		fetchErr = worktree.Fetch(fctx, a.RepoRoot)
		cancel()
	}
	var missing []string
	postWorkHere := ws.PostWorkHead != ""
	for _, h := range heads {
		p := probeHead(a.RepoRoot, h)
		if p.err != nil {
			return &takeoverCheck{name, fmt.Sprintf("could not check for commit %s: %v", shortHead(h), p.err), "run it from the story's repository"}
		}
		if !p.inRepo {
			missing = append(missing, shortHead(h))
			if h == ws.PostWorkHead {
				postWorkHere = false
			}
		}
	}
	var failures []string
	fix := "have the holder push its branch, then `git fetch` here"
	if len(missing) > 0 {
		failure := fmt.Sprintf("commit %s the story's ledger claims is not in this repository", strings.Join(missing, ", "))
		if fetchErr != nil {
			failure += fmt.Sprintf(" (the fetch failed: %v)", fetchErr)
		}
		failures = append(failures, failure)
	}
	if ws.Started() && !postWorkHere {
		failures = append(failures, "work started on the holder but no commit after it is reachable here; its code may exist only in the holder's tree")
		fix = fmt.Sprintf("have the holder commit and push its branch, then `git fetch` and `satelle sync workstate pull %s` here", id)
	}
	if len(failures) == 0 {
		return nil
	}
	return &takeoverCheck{name, strings.Join(failures, "; "), fix}
}

func shortHead(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// forceTakeoverPayload is the ledger payload of a takeover that overrode failed
// checks.
type forceTakeoverPayload struct {
	Event  string          `json:"event"`
	Reason string          `json:"reason"`
	Failed []takeoverCheck `json:"failed"`
}

// recordForcedTakeover writes the override into the story's ledger, so the
// synced record says who bypassed which check and why.
func recordForcedTakeover(ctx context.Context, a *app.App, id, reason string, failed []takeoverCheck) error {
	payload, err := json.Marshal(forceTakeoverPayload{Event: "hold_takeover_forced", Reason: reason, Failed: failed})
	if err != nil {
		return err
	}
	var names []string
	for _, c := range failed {
		names = append(names, c.Name)
	}
	_, err = a.Store.Ledger.Append(ctx, ledger.AppendInput{
		StoryID: id, Kind: ledger.KindComment, Actor: takeoverActor,
		Body:    fmt.Sprintf("hold takeover forced past failed check(s) %s: %s", strings.Join(names, ", "), reason),
		Payload: payload,
	}, time.Now().UTC())
	return err
}
