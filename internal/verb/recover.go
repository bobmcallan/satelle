package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/logsread"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// story-recover (sty_f0ed2473) answers "what did a dispatch that died leave?".
// It REPORTS and never VOUCHES: everything it prints is read from evidence the
// store already holds (the dispatch log, the lease, the engagement anchor's git
// slice, an agent-stalled row) and none of it is a claim that the work is
// coherent. With --choice it records the driver's recovery decision as one
// ledger row and stops — it never dispatches, transitions or parks. No live
// session is persisted or re-entered.

// Recovery choices the driver may record. The set is closed: the report names
// them and the row carries exactly one.
const (
	recoverRedispatch = "redispatch"
	recoverFinish     = "finish"
	recoverPark       = "park"
)

var recoveryChoices = []string{recoverRedispatch, recoverFinish, recoverPark}

// Recovery states: what the newest dispatch log says about its dispatch.
const (
	recoverStateNone     = "none"                     // no dispatch log, or it ends in a completion
	recoverStateEnded    = "ended-without-completion" // the log stops short of a completed event
	recoverStateInFlight = "in-flight"                // the lease says the transition is still running
)

// recoverHeader is the fixed first line of every report about a dispatch that
// ended without a completion. It is mechanism wording, never composed from the
// work: the transition did not commit and nothing here verifies the slice.
const recoverHeader = "dispatch %s ended WITHOUT a completion; the transition did NOT commit; the work is UNVERIFIED"

func init() {
	Register(&Verb{
		Name:        "story-recover",
		Description: "Report what a dispatch that ended without a completion left, or record the driver's recovery choice (report only; never resumes or transitions)",
		Invoke:      storyRecover,
	})
}

// dispatchLogsDir resolves the directory holding this repo's dispatch logs: the
// cwd's .satelle/logs pointer, else the SATELLE_HOME-resolved logs dir. Empty
// when neither is available. A variable so tests can point it at a temp dir.
var dispatchLogsDir = func() string {
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, ".satelle", "logs")
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return p
		}
	}
	if strings.TrimSpace(os.Getenv("SATELLE_HOME")) != "" {
		cfg, cfgPath, err := config.Load("")
		if err == nil {
			return cfg.ResolveLogsDir(config.RepoRootFromConfigPath(cfgPath))
		}
	}
	return ""
}

type recoverReq struct {
	ID     string `json:"id"`
	Choice string `json:"choice,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// RecoverResult is the verb's answer: the report when no choice was given, the
// recorded choice (and its ledger row) when one was.
type RecoverResult struct {
	StoryID string `json:"story_id"`
	State   string `json:"state"`
	Report  string `json:"report"`
	// Choice and LedgerID are set only when a recovery_choice row was written.
	Choice   string `json:"choice,omitempty"`
	LedgerID string `json:"ledger_id,omitempty"`
}

// recoveryLog is the newest dispatch log for the story.
type recoveryLog struct {
	Path     string
	Agent    string
	StartNS  int64
	Mtime    time.Time
	LastLine string // last non-empty line; empty when the log has no events
}

// recoveryStall is an agent-stalled telemetry row written by the watchdog,
// quoted as corroboration and never as a reason to stay quiet.
type recoveryStall struct {
	LastEvent   string
	LastEventAt string
	Idle        string
}

// recoveryInput is everything the report reads, assembled by gatherRecovery so
// the renderer stays pure.
type recoveryInput struct {
	StoryID string
	Now     time.Time
	Log     *recoveryLog
	Stall   *recoveryStall
	Lease   *lease.Lease
	Files   []string
	// Anchor is the git sha the file list is measured from; FilesErr is why the
	// list is unavailable (no baseline, no git, foreign tree).
	Anchor   string
	FilesErr string
}

// lastEventKind is the normalized kind of a dispatch-log line
// (agentcli.FormatEvent: "<rfc3339>\t<kind>\t<details>"); empty when the line
// is not in that shape.
func lastEventKind(line string) string {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// isUnfinished reports whether a dispatch log's last event line stops short of a
// completion. The normalized completed kind is emitted by every adapter (claude
// stream, grok acp, command), so this reads no provider format. Anything that is
// not a completed event — an open tool call, a failure, an empty log — is
// unfinished; erring that way is the safe side, since the report never vouches.
func isUnfinished(lastLine string) bool {
	return lastEventKind(lastLine) != string(agentcli.EventCompleted)
}

// lastEventAt is when the last event line was recorded: its own timestamp when
// parseable, else the log's mtime.
func (l recoveryLog) lastEventAt() time.Time {
	if f := strings.SplitN(l.LastLine, "\t", 2); len(f) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, f[0]); err == nil {
			return t
		}
	}
	return l.Mtime
}

// recoveryState classifies the evidence. In flight wins over an unfinished log:
// a live transition also has no completed line yet.
func recoveryState(in recoveryInput) string {
	if in.Log == nil || !isUnfinished(in.Log.LastLine) {
		return recoverStateNone
	}
	if in.Lease != nil && lease.EffectiveInFlight(*in.Lease, in.Now) {
		return recoverStateInFlight
	}
	return recoverStateEnded
}

// renderRecoveryReport renders the report. It states what was recorded and what
// was not; it has no path that says the work is complete.
func renderRecoveryReport(in recoveryInput) string {
	var b strings.Builder
	switch recoveryState(in) {
	case recoverStateNone:
		if in.Log == nil {
			fmt.Fprintf(&b, "no unfinished dispatch found for %s (no dispatch log)\n", in.StoryID)
		} else {
			fmt.Fprintf(&b, "no unfinished dispatch found for %s (newest log %s ends in a completion)\n",
				in.StoryID, filepath.Base(in.Log.Path))
		}
		return b.String()
	case recoverStateInFlight:
		fmt.Fprintf(&b, "dispatch %s is still in flight (pid %d); nothing has ended, so there is nothing to recover yet\n",
			in.Log.Agent, in.Lease.InFlightPid)
		writeRecoveryEvent(&b, in)
		return b.String()
	}

	fmt.Fprintf(&b, recoverHeader+"\n", in.Log.Agent)
	b.WriteString("\nlast dispatch\n")
	fmt.Fprintf(&b, "  log: %s\n", in.Log.Path)
	writeRecoveryEvent(&b, in)
	if s := in.Stall; s != nil {
		fmt.Fprintf(&b, "  watchdog: agent-stalled after %s idle (last event %q at %s)\n", s.Idle, s.LastEvent, s.LastEventAt)
	}
	if l := in.Lease; l != nil {
		switch {
		case !l.InFlight:
			b.WriteString("  lease: not in flight\n")
		case l.InFlightPid > 0:
			fmt.Fprintf(&b, "  lease: in flight, pid %d not running\n", l.InFlightPid)
		default:
			fmt.Fprintf(&b, "  lease: in flight, heartbeat aged %s\n", in.Now.Sub(l.HeartbeatAt).Round(time.Second))
		}
	}

	b.WriteString("\nfiles present since the engagement anchor\n")
	switch {
	case in.FilesErr != "":
		fmt.Fprintf(&b, "  unavailable (%s)\n", in.FilesErr)
	case len(in.Files) == 0:
		fmt.Fprintf(&b, "  none changed since %s (enumeration; not attributed per-dispatch)\n", shortSHA(in.Anchor))
	default:
		fmt.Fprintf(&b, "  written or edited since %s (enumeration; not attributed per-dispatch):\n", shortSHA(in.Anchor))
		for _, f := range in.Files {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}

	b.WriteString("\nthis report reads recorded evidence; it does not say whether the slice is coherent. Decide, then record it:\n")
	fmt.Fprintf(&b, "  satelle story recover %s --choice %s\n", in.StoryID, strings.Join(recoveryChoices, "|"))
	return b.String()
}

func writeRecoveryEvent(b *strings.Builder, in recoveryInput) {
	if in.Log.LastLine == "" {
		fmt.Fprintf(b, "  last event: none recorded (log is empty)\n")
		return
	}
	at := in.Log.lastEventAt()
	since := "unknown"
	if !at.IsZero() {
		since = in.Now.Sub(at).Round(time.Second).String()
	}
	fmt.Fprintf(b, "  last event: %s\n  wall time since: %s\n", strings.ReplaceAll(in.Log.LastLine, "\t", " "), since)
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// gatherRecovery reads the evidence for one story. Every source is best-effort:
// a source that is missing shows as absent in the report, never as an error.
func gatherRecovery(ctx context.Context, it workitem.Item, now time.Time) recoveryInput {
	in := recoveryInput{StoryID: it.ID, Now: now}
	if dir := dispatchLogsDir(); dir != "" {
		if f, ok, err := logsread.Select(dir, it.ID, ""); err == nil && ok {
			log := &recoveryLog{Path: f.Path, Agent: f.Agent, StartNS: f.Nano, Mtime: f.Mtime}
			if raw, rerr := os.ReadFile(f.Path); rerr == nil {
				if lines := logsread.LastNLines(string(raw), 1); len(lines) == 1 {
					log.LastLine = lines[0]
				}
			}
			in.Log = log
		}
	}
	if ls, err := requireLease(); err == nil {
		if l, gerr := ls.Get(ctx, it.ID); gerr == nil {
			in.Lease = &l
		}
	}
	if in.Log != nil {
		in.Stall = latestStall(ctx, it.ID, in.Log.StartNS)
	}
	if res, err := liveStoryDiff(ctx, it, false, false); err != nil {
		in.FilesErr = err.Error()
	} else {
		in.Files, in.Anchor = res.Files, res.Baseline
	}
	return in
}

// latestStall returns the newest agent-stalled telemetry row written at or after
// startNS (the dispatch log's start), or nil.
func latestStall(ctx context.Context, storyID string, startNS int64) *recoveryStall {
	ls, err := requireLedger()
	if err != nil {
		return nil
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindTelemetryEvent)
	if err != nil {
		return nil
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.CreatedAt.UnixNano() < startNS {
			break
		}
		var env struct {
			Kind string `json:"kind"`
			Data struct {
				Idle        string `json:"idle"`
				LastEvent   string `json:"last_event"`
				LastEventAt string `json:"last_event_at"`
			} `json:"data"`
		}
		if json.Unmarshal(e.Payload, &env) != nil || env.Kind != "agent-stalled" {
			continue
		}
		return &recoveryStall{LastEvent: env.Data.LastEvent, LastEventAt: env.Data.LastEventAt, Idle: env.Data.Idle}
	}
	return nil
}

// recoveryChoicePayload is the KindRecoveryChoice ledger payload.
type recoveryChoicePayload struct {
	Choice    string `json:"choice"`
	Reason    string `json:"reason,omitempty"`
	State     string `json:"state"`
	Log       string `json:"log,omitempty"`
	LastEvent string `json:"last_event,omitempty"`
	FileCount int    `json:"file_count"`
}

func storyRecover(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req recoverReq
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("story-recover: id required")
	}
	req.Choice = strings.ToLower(strings.TrimSpace(req.Choice))
	if req.Choice != "" && !validRecoveryChoice(req.Choice) {
		return nil, fmt.Errorf("story-recover: unknown choice %q — one of %s", req.Choice, strings.Join(recoveryChoices, ", "))
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if it.Kind != workitem.KindStory {
		return nil, fmt.Errorf("story-recover: %s is not a story", req.ID)
	}

	now := time.Now()
	in := gatherRecovery(ctx, it, now)
	res := RecoverResult{StoryID: it.ID, State: recoveryState(in), Report: renderRecoveryReport(in)}
	if req.Choice == "" {
		return json.Marshal(res)
	}

	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	p := recoveryChoicePayload{Choice: req.Choice, Reason: strings.TrimSpace(req.Reason), State: res.State, FileCount: len(in.Files)}
	if in.Log != nil {
		p.Log = filepath.Base(in.Log.Path)
		p.LastEvent = strings.ReplaceAll(in.Log.LastLine, "\t", " ")
	}
	payload, _ := json.Marshal(p)
	body := fmt.Sprintf("recovery choice: %s (dispatch state: %s)", req.Choice, res.State)
	if p.Reason != "" {
		body += " — " + p.Reason
	}
	e, err := ls.Append(ctx, ledger.AppendInput{
		StoryID: it.ID,
		Kind:    ledger.KindRecoveryChoice,
		Actor:   "executor",
		Body:    body,
		Payload: payload,
	}, now)
	if err != nil {
		return nil, fmt.Errorf("story-recover: record choice: %w", err)
	}
	res.Choice, res.LedgerID = req.Choice, e.ID
	res.Report = body + "\nrecorded only: nothing was dispatched, transitioned or parked; act through the normal verbs\n"
	return json.Marshal(res)
}

func validRecoveryChoice(c string) bool {
	for _, v := range recoveryChoices {
		if c == v {
			return true
		}
	}
	return false
}
