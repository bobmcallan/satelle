// Package fixlane is the scoped in-loop fix lane (sty_4b694872): a driver may
// record a typed in-loop-fix claim and make ONE small edit without a full
// engage. The lane changes WHO MAY EDIT, never WHAT IS JUDGED — the fix is still
// judged by the normal gate on the step's own edge.
//
// Three rules make it a lane rather than a loophole:
//
//  1. The bound is ABSOLUTE and is configuration. A claim touching product
//     surface, a gate skill, a reviewer rubric, a workflow, a principle or the
//     constitution, or naming no proving test, is refused. The path classes and
//     the size ceiling come from [fix_lane] (embedded default plus the repo's
//     own); nothing here is a bound literal.
//  2. Every claim, granted or refused, is a ledger row written BEFORE the edit,
//     so the exception rate, its size distribution and its refusals are
//     measurable. An unbounded lane is indistinguishable from no lane.
//  3. A claim is never a standing licence: it is consumed by ONE allowed edit
//     and dead at the story's next transition.
package fixlane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// Refused classes. A refused claim's ledger row and the error it returns both
// name one of these, so refusal is analysable by class.
const (
	ClassPrinciple       = "principle"        // a principle or the constitution
	ClassWorkflow        = "workflow"         // a workflow definition
	ClassGateSkill       = "gate-skill"       // a gate's skill
	ClassReviewerRubric  = "reviewer-rubric"  // the agents layer binding a reviewer's rubric and grant
	ClassRepoConfig      = "repo-config"      // satelle.toml / satelle.local.toml, which carry the bound
	ClassProductSurface  = "product-surface"  // the shipped product
	ClassNoProvingTest   = "no-proving-test"  // no named test proves the fix
	ClassNoReason        = "no-reason"        // no reason recorded
	ClassNoBound         = "no-bound"         // no positive size bound declared
	ClassOverBound       = "over-bound"       // declared size exceeds the configured ceiling
	ClassUndeclaredBound = "undeclared-bound" // the repo declares no product surface: lane closed
	ClassNoStory         = "no-engaged-story" // no engaged story for the edge to judge the fix
	ClassOutsideRepo     = "outside-repo"     // the path is not a file inside this repo
)

// Decisions on a fix_claim row.
const (
	DecisionRecorded = "recorded"
	DecisionRefused  = "refused"
)

// Input is one claim as the driver states it.
type Input struct {
	StoryID     string // the engaged story the fix is judged under
	Status      string // that story's committed status at claim time
	Path        string // the file the fix edits (absolute or repo-relative)
	Reason      string
	BoundLines  int    // the declared size bound, in changed lines
	ProvingTest string // the named test that proves the fix
	Actor       string
}

// ClaimPayload is the payload of a fix_claim row: path, reason, bound and
// proving test — the four things the analysis needs — plus the decision.
type ClaimPayload struct {
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	BoundLines   int    `json:"bound_lines"`
	ProvingTest  string `json:"proving_test"`
	Status       string `json:"status,omitempty"`
	Decision     string `json:"decision"`
	RefusedClass string `json:"refused_class,omitempty"`
}

// UsePayload is the payload of a fix_claim_used row: the edit the claim licensed.
type UsePayload struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
}

// Claim is a recorded (granted) claim read back from the ledger.
type Claim struct {
	ID        string
	StoryID   string
	CreatedAt time.Time
	ClaimPayload
}

// Refusal is the error a refused claim returns; it names the class.
type Refusal struct {
	Class  string
	Path   string
	Detail string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("fix lane: claim refused (class %s) for %s: %s", r.Class, r.Path, r.Detail)
}

// RelPath returns p as a slash-separated path relative to repoRoot, and whether
// p is a file inside it. An absolute path outside the repo, or one that climbs
// out of it, is not.
func RelPath(repoRoot, p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if filepath.IsAbs(p) {
		root := repoRoot
		if abs, err := filepath.Abs(repoRoot); err == nil {
			root = abs
		}
		rel, err := filepath.Rel(root, filepath.Clean(p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// The same tree reached through a symlink (a cwd under a linked dir):
			// compare real paths before calling it outside.
			realRoot, e1 := filepath.EvalSymlinks(root)
			realP, e2 := filepath.EvalSymlinks(filepath.Dir(p))
			if e1 != nil || e2 != nil {
				return "", false
			}
			rel, err = filepath.Rel(realRoot, filepath.Join(realP, filepath.Base(p)))
			if err != nil {
				return "", false
			}
		}
		p = rel
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}

// Match reports whether rel (slash-separated, repo-relative) matches glob. A
// glob without a "/" matches a basename at any depth; otherwise it is matched
// segment by segment against the repo-relative path, "**" standing for any run
// of segments (including none). Blank and malformed globs never match — a bad
// glob must not fail open toward matching everything.
func Match(glob, rel string) bool {
	glob = strings.TrimSpace(glob)
	if glob == "" || rel == "" {
		return false
	}
	if !strings.Contains(glob, "/") {
		ok, err := path.Match(glob, path.Base(rel))
		return err == nil && ok
	}
	return matchSegments(strings.Split(glob, "/"), strings.Split(rel, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return len(segs) > 0 // "dir/**" needs something beneath dir
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

func matchAny(globs []string, rel string) bool {
	for _, g := range globs {
		if Match(g, rel) {
			return true
		}
	}
	return false
}

// Classify applies the configured bound to one claim (rel already normalised)
// and returns the refused class and a detail line, or "" when the claim is
// within the bound. The order is fixed so a claim failing several rules is
// always counted under the same class: the bound's absence first, then the
// path classes from the most protected outward, then the claim's own fields.
func Classify(cfg config.FixLaneConfig, in Input, rel string) (class, detail string) {
	if len(cfg.ProductSurface) == 0 || cfg.MaxLines <= 0 {
		return ClassUndeclaredBound, "no product surface / size ceiling is declared in [fix_lane] — a lane with no declared bound is closed, not open"
	}
	if strings.TrimSpace(in.StoryID) == "" {
		return ClassNoStory, "no story is engaged, so no edge would judge the fix — engage a story first"
	}
	// Most specific first: agents.toml sits inside the workflows root, and
	// satelle.toml inside the data dir, so the narrower class must be tested
	// before the tree that contains it or it could never be reported.
	switch {
	case matchAny(cfg.RepoConfig, rel):
		return ClassRepoConfig, rel + " is repo configuration — it carries this very bound"
	case matchAny(cfg.Principles, rel):
		return ClassPrinciple, rel + " is a principle or the constitution"
	case matchAny(cfg.ReviewerRubrics, rel):
		return ClassReviewerRubric, rel + " binds a reviewer's rubric and grant"
	case matchAny(cfg.Workflows, rel):
		return ClassWorkflow, rel + " is a workflow definition"
	case matchAny(cfg.GateSkills, rel):
		return ClassGateSkill, rel + " is a gate skill"
	case matchAny(cfg.ProductSurface, rel):
		return ClassProductSurface, rel + " is product surface"
	}
	switch {
	case strings.TrimSpace(in.ProvingTest) == "":
		return ClassNoProvingTest, "name the test that proves the fix (--test)"
	case strings.TrimSpace(in.Reason) == "":
		return ClassNoReason, "state why the fix is self-evident (--reason)"
	case in.BoundLines <= 0:
		return ClassNoBound, "declare the size bound in changed lines (--lines)"
	case in.BoundLines > cfg.MaxLines:
		return ClassOverBound, fmt.Sprintf("declared %d lines exceeds the configured ceiling of %d", in.BoundLines, cfg.MaxLines)
	}
	return "", ""
}

// Record judges the claim against the configured bound and appends its ledger
// row — granted or refused — BEFORE returning, so the row exists before any edit
// it could license. A refused claim returns a *Refusal naming the class; the
// refusal row has already been written by then.
func Record(ctx context.Context, ls *ledger.Store, cfg config.Config, repoRoot string, in Input, now time.Time) (Claim, error) {
	if ls == nil {
		return Claim{}, fmt.Errorf("fix lane: ledger unavailable — no claim can be recorded, so none is granted")
	}
	rel, inRepo := RelPath(repoRoot, in.Path)
	class, detail := "", ""
	if !inRepo {
		rel, class, detail = strings.TrimSpace(in.Path), ClassOutsideRepo, "the path is not a file inside this repo"
	} else {
		class, detail = Classify(cfg.ResolveFixLane(repoRoot), in, rel)
	}
	p := ClaimPayload{
		Path: rel, Reason: strings.TrimSpace(in.Reason), BoundLines: in.BoundLines,
		ProvingTest: strings.TrimSpace(in.ProvingTest), Status: in.Status,
		Decision: DecisionRecorded,
	}
	body := fmt.Sprintf("in-loop-fix claim %s (bound %d lines, proof %s): %s", rel, in.BoundLines, p.ProvingTest, p.Reason)
	if class != "" {
		p.Decision, p.RefusedClass = DecisionRefused, class
		body = fmt.Sprintf("in-loop-fix claim REFUSED (%s) %s: %s", class, rel, detail)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return Claim{}, err
	}
	actor := in.Actor
	if actor == "" {
		actor = "executor"
	}
	e, err := ls.Append(ctx, ledger.AppendInput{
		StoryID: in.StoryID, Kind: ledger.KindFixClaim, Actor: actor, Body: body, Payload: raw,
	}, now)
	if err != nil {
		return Claim{}, fmt.Errorf("fix lane: record claim: %w", err)
	}
	if class != "" {
		return Claim{}, &Refusal{Class: class, Path: rel, Detail: detail}
	}
	return Claim{ID: e.ID, StoryID: e.StoryID, CreatedAt: e.CreatedAt, ClaimPayload: p}, nil
}

// Live returns the oldest claim on storyID for rel that still licenses an edit:
// recorded (not refused), not yet consumed by a fix_claim_used row, and recorded
// after the story's most recent status_transition — a transition kills every
// claim recorded before it. A standing licence is the failure mode the lane
// exists to prevent.
func Live(ctx context.Context, ls *ledger.Store, storyID, rel string) (Claim, bool, error) {
	live, _, err := storyClaims(ctx, ls, storyID)
	if err != nil {
		return Claim{}, false, err
	}
	for _, c := range live {
		if c.Path == rel {
			return c, true, nil
		}
	}
	return Claim{}, false, nil
}

// LastRefusal returns the refused class of the story's most recent claim on rel
// when that claim was refused, so an edit denial can name why the lane did not
// open. Empty when there is none.
func LastRefusal(ctx context.Context, ls *ledger.Store, storyID, rel string) (string, error) {
	rows, err := ls.ListByStory(ctx, storyID, ledger.KindFixClaim)
	if err != nil {
		return "", err
	}
	class := ""
	for _, e := range rows {
		var p ClaimPayload
		if json.Unmarshal(e.Payload, &p) != nil || p.Path != rel {
			continue
		}
		class = p.RefusedClass
	}
	return class, nil
}

// storyClaims reads the story's fix_claim rows and returns those still live
// (recorded, unconsumed, no later transition), oldest first.
func storyClaims(ctx context.Context, ls *ledger.Store, storyID string) (live []Claim, all []Claim, err error) {
	if ls == nil || strings.TrimSpace(storyID) == "" {
		return nil, nil, nil
	}
	rows, err := ls.ListByStory(ctx, storyID, "")
	if err != nil {
		return nil, nil, err
	}
	consumed := map[string]bool{}
	var lastTransition time.Time
	for _, e := range rows {
		switch e.Kind {
		case ledger.KindFixClaimUse:
			var refs struct {
				Claim string `json:"claim"`
			}
			if json.Unmarshal(e.Refs, &refs) == nil && refs.Claim != "" {
				consumed[refs.Claim] = true
			}
		case ledger.KindStatusTransition:
			if e.CreatedAt.After(lastTransition) {
				lastTransition = e.CreatedAt
			}
		}
	}
	for _, e := range rows {
		if e.Kind != ledger.KindFixClaim {
			continue
		}
		c := Claim{ID: e.ID, StoryID: e.StoryID, CreatedAt: e.CreatedAt}
		if json.Unmarshal(e.Payload, &c.ClaimPayload) != nil {
			continue
		}
		all = append(all, c)
		if c.Decision == DecisionRecorded && !consumed[c.ID] && (lastTransition.IsZero() || e.CreatedAt.After(lastTransition)) {
			live = append(live, c)
		}
	}
	return live, all, nil
}

// ErrConsumed is returned by Consume when the claim already has a use row —
// another edit got there first. The caller must refuse the edit.
var ErrConsumed = errors.New("fix lane: claim already consumed by another edit")

// Consume appends the fix_claim_used row for the ONE edit claim licensed.
// Called before the edit is allowed, so the row precedes the change; a claim
// with a use row is never live again. The append is atomic per claim
// (ledger.AppendOnce): of any number of concurrent edits racing on one claim,
// exactly one gets nil and the rest get ErrConsumed.
func Consume(ctx context.Context, ls *ledger.Store, c Claim, lines int, now time.Time) error {
	raw, err := json.Marshal(UsePayload{Path: c.Path, Lines: lines})
	if err != nil {
		return err
	}
	refs, err := json.Marshal(map[string]string{"claim": c.ID})
	if err != nil {
		return err
	}
	_, inserted, err := ls.AppendOnce(ctx, ledger.AppendInput{
		StoryID: c.StoryID, Kind: ledger.KindFixClaimUse, Actor: "executor",
		Body:    fmt.Sprintf("in-loop-fix claim %s consumed by an edit of %s (%d lines)", c.ID, c.Path, lines),
		Payload: raw, Refs: refs,
	}, now)
	if err != nil {
		return err
	}
	if !inserted {
		return ErrConsumed
	}
	return nil
}
