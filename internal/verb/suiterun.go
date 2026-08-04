package verb

// SHA-keyed shared suite evidence (sty_183a0510): record one run of an
// expensive verification suite, let sibling stories CITE it, and enumerate the
// facts a gate needs to judge a citation.
//
// Enumeration only — no pass/fail in Go. `ledger-citation` reports what is true
// (is there a citation, does it resolve, was the run green, what command ran,
// does its sha match a clean HEAD) and exits 0 for every enumerable state; the
// gate's check block names the refusal reasons and decides. Nonzero is reserved
// for genuine errors (no store, unknown story, git unavailable), matching
// story-diff.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

func init() {
	Register(&Verb{
		Name:        "ledger-record-run",
		Description: "Record a verification-suite run as SHA-keyed evidence a sibling story can cite",
		Invoke:      ledgerRecordRun,
	})
	Register(&Verb{
		Name:        "ledger-cite-run",
		Description: "Cite a recorded suite run from another story (writes the citation's Refs)",
		Invoke:      ledgerCiteRun,
	})
	Register(&Verb{
		Name:        "ledger-citation",
		Description: "Enumerate a story's suite-run citation and the facts a gate needs (enumeration only)",
		Invoke:      ledgerCitation,
	})
}

// suiteRunPayload is the ledger payload for KindSuiteRun. Timestamps are the
// caller's own (RFC3339 by convention, but stored verbatim — the recording
// harness owns their format).
type suiteRunPayload struct {
	SHA        string `json:"sha"`
	Command    string `json:"command"`
	Outcome    string `json:"outcome"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// suiteRunRefs is the Refs shape of a KindSuiteCitation row — the first write
// of Entry.Refs, which has been plumbed through store and verb since the
// ledger landed.
type suiteRunRefs struct {
	SuiteRun string `json:"suite_run"`
}

// Legal outcomes. A value outside this set is a request error (the same class
// as a missing kind), not a gate verdict: the ledger records what a run WAS,
// and "green"/"red" is the vocabulary every reader shares.
const (
	suiteOutcomeGreen = "green"
	suiteOutcomeRed   = "red"
)

// suiteRunRecordReq is the request body for ledger-record-run.
type suiteRunRecordReq struct {
	StoryID    string `json:"story_id"`
	SHA        string `json:"sha,omitempty"`
	Command    string `json:"command"`
	Outcome    string `json:"outcome"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func ledgerRecordRun(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	items, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req suiteRunRecordReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	req.StoryID = strings.TrimSpace(req.StoryID)
	req.Command = strings.TrimSpace(req.Command)
	req.Outcome = strings.TrimSpace(req.Outcome)
	if req.StoryID == "" {
		return nil, fmt.Errorf("verb: record-run requires the recording story id")
	}
	if req.Command == "" {
		return nil, fmt.Errorf("verb: record-run requires --command (the suite command that ran)")
	}
	if req.Outcome != suiteOutcomeGreen && req.Outcome != suiteOutcomeRed {
		return nil, fmt.Errorf("verb: record-run --outcome must be %q or %q, got %q", suiteOutcomeGreen, suiteOutcomeRed, req.Outcome)
	}
	it, err := items.Get(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}

	sha := strings.TrimSpace(req.SHA)
	dirty := false
	if sha == "" {
		head, d, gerr := gitHeadAndDirty(workingDir())
		if gerr != nil {
			return nil, fmt.Errorf("verb: record-run could not resolve HEAD (pass --sha): %w", gerr)
		}
		sha, dirty = head, d
	}

	payload, err := json.Marshal(suiteRunPayload{
		SHA:        sha,
		Command:    req.Command,
		Outcome:    req.Outcome,
		StartedAt:  strings.TrimSpace(req.StartedAt),
		FinishedAt: strings.TrimSpace(req.FinishedAt),
	})
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("suite run %s at %s: %s", req.Outcome, sha, req.Command)
	if dirty {
		body += " (recorded from a dirty worktree)"
	}
	e, err := ls.Append(ctx, ledger.AppendInput{
		StoryID: it.ID,
		Kind:    ledger.KindSuiteRun,
		Actor:   "executor",
		Body:    body,
		Payload: payload,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	appendOpLog("ledger-record-run", it.ID, fmt.Sprintf("suite run %s recorded at %s", req.Outcome, sha), time.Now())
	return json.Marshal(e)
}

// suiteRunCiteReq is the request body for ledger-cite-run.
type suiteRunCiteReq struct {
	StoryID string `json:"story_id"`
	RunID   string `json:"run_id"`
}

// ledgerCiteRun records that a story rides a suite run recorded elsewhere. The
// cited id is deliberately NOT validated here: a dangling citation is a fact a
// gate must be able to see (and test), not an error the writer hides.
func ledgerCiteRun(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	items, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req suiteRunCiteReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	req.StoryID = strings.TrimSpace(req.StoryID)
	req.RunID = strings.TrimSpace(req.RunID)
	if req.StoryID == "" {
		return nil, fmt.Errorf("verb: cite-run requires the citing story id")
	}
	if req.RunID == "" {
		return nil, fmt.Errorf("verb: cite-run requires --run (the suite_run entry id)")
	}
	it, err := items.Get(ctx, req.StoryID)
	if err != nil {
		return nil, err
	}
	refs, err := json.Marshal(suiteRunRefs{SuiteRun: req.RunID})
	if err != nil {
		return nil, err
	}
	e, err := ls.Append(ctx, ledger.AppendInput{
		StoryID: it.ID,
		Kind:    ledger.KindSuiteCitation,
		Actor:   "executor",
		Body:    fmt.Sprintf("cites suite run %s", req.RunID),
		Refs:    refs,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	appendOpLog("ledger-cite-run", it.ID, fmt.Sprintf("cites suite run %s", req.RunID), time.Now())
	return json.Marshal(e)
}

// citationRun is the cited run's facts, echoed verbatim from its payload.
type citationRun struct {
	StoryID    string `json:"story_id,omitempty"`
	SHA        string `json:"sha,omitempty"`
	Command    string `json:"command,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

// citationResult is the deterministic enumeration result (no verdict). Every
// field a gate's check block needs is present in one shot, so no refusal
// ORDERING policy exists here either — the gate reports whichever fact it cares
// about first.
type citationResult struct {
	StoryID        string       `json:"story_id"`
	Citations      int          `json:"citations"`
	Cited          bool         `json:"cited"`
	CitationID     string       `json:"citation_id,omitempty"`
	RunID          string       `json:"run_id,omitempty"`
	RunFound       bool         `json:"run_found"`
	Dangling       bool         `json:"dangling"`
	Run            *citationRun `json:"run,omitempty"`
	HeadSHA        string       `json:"head_sha"`
	Dirty          bool         `json:"dirty"`
	SHAMatchesHead bool         `json:"sha_matches_head"`
	Note           string       `json:"note"`
}

// citationReq is the request for ledger-citation: an explicit id, or the piped
// transition payload a functional check receives on stdin.
type citationReq struct {
	ID      string `json:"id"`
	StoryID string `json:"story_id"`
	Story   struct {
		ID string `json:"id"`
	} `json:"story"`
}

func ledgerCitation(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	items, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req citationReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	// Gate functional checks pipe {story, from, to} with no argv id — the same
	// stdin tolerance story-diff carries.
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = strings.TrimSpace(req.StoryID)
	}
	if id == "" {
		id = strings.TrimSpace(req.Story.ID)
	}
	if id == "" {
		return nil, fmt.Errorf("ledger-citation: id required (pass <id> or pipe the transition payload with story.id on stdin)")
	}
	it, err := items.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	head, dirty, gerr := gitHeadAndDirty(workingDir())
	if gerr != nil {
		return nil, fmt.Errorf("ledger-citation: %w", gerr)
	}

	res := citationResult{
		StoryID: it.ID,
		HeadSHA: head,
		Dirty:   dirty,
		Note:    "enumeration only — no pass/fail; gates decide",
	}

	cites, err := ls.ListByStory(ctx, it.ID, ledger.KindSuiteCitation)
	if err != nil {
		return nil, err
	}
	res.Citations = len(cites)
	if len(cites) == 0 {
		return json.Marshal(res)
	}
	// Newest citation wins: a story that cites again has corrected itself.
	latest := cites[len(cites)-1]
	res.Cited = true
	res.CitationID = latest.ID
	var refs suiteRunRefs
	if len(latest.Refs) > 0 {
		_ = json.Unmarshal(latest.Refs, &refs)
	}
	res.RunID = strings.TrimSpace(refs.SuiteRun)
	if res.RunID == "" {
		res.Dangling = true
		return json.Marshal(res)
	}

	entry, ok, err := ls.GetByID(ctx, res.RunID)
	if err != nil {
		return nil, err
	}
	if !ok || entry.Kind != ledger.KindSuiteRun {
		res.Dangling = true
		return json.Marshal(res)
	}
	var p suiteRunPayload
	if len(entry.Payload) > 0 {
		_ = json.Unmarshal(entry.Payload, &p)
	}
	res.RunFound = true
	res.Run = &citationRun{
		StoryID:    entry.StoryID,
		SHA:        p.SHA,
		Command:    p.Command,
		Outcome:    p.Outcome,
		StartedAt:  p.StartedAt,
		FinishedAt: p.FinishedAt,
		RecordedAt: entry.CreatedAt.UTC().Format(time.RFC3339),
	}
	res.SHAMatchesHead = p.SHA != "" && p.SHA == head
	return json.Marshal(res)
}

// workingDir is the process cwd, falling back to "." — the same resolution the
// engagement baseline and change record use.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}
