package verb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// Minimal seam stubs — the predicates only read whether a seam is wired.
type stubGater struct{}

func (stubGater) Gate(context.Context, workitem.Item, string) (GateDecision, error) {
	return GateDecision{}, nil
}

type stubCreateReviewer struct{}

func (stubCreateReviewer) ReviewCreate(context.Context, CreateDraft) (GateDecision, error) {
	return GateDecision{}, nil
}

type stubAmendReviewer struct{}

func (stubAmendReviewer) ReviewAmend(context.Context, AmendDraft) (GateDecision, error) {
	return GateDecision{}, nil
}

// reviewerSeamCall matches a call THROUGH one of the seams that runs a reviewer
// or another isolated agent.
var reviewerSeamCall = regexp.MustCompile(`\b(transitionGater|createReviewer|amendReviewer|executorDispatcher|retrospector|stepSummariser)\.[A-Z]\w*\(`)

// reviewerDispatchSites names, per source file, the verbs whose invocation
// reaches a reviewer seam from it. A file that calls a seam and is not here — or
// a verb named here that does not declare DispatchesReviewer — fails: that is a
// verb an agent-facing caller would sit through unbounded (sty_c4b92c9e AC6).
var reviewerDispatchSites = map[string][]string{
	"workitem.go": {
		"story-create", "task-create", "execution-create",
		"story-set", "task-set", "execution-set",
		"story-resummarise", "story-retrospect",
	},
	"amend.go": {"story-amend"},
}

func TestEveryReviewerDispatchSiteDeclaresItself(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Comments mention the seams by name; only code counts.
		var code []string
		for _, ln := range strings.Split(string(src), "\n") {
			if i := strings.Index(ln, "//"); i >= 0 {
				ln = ln[:i]
			}
			code = append(code, ln)
		}
		if reviewerSeamCall.MatchString(strings.Join(code, "\n")) {
			found[f] = true
		}
	}
	for f := range found {
		verbs, ok := reviewerDispatchSites[f]
		if !ok {
			t.Errorf("%s calls a reviewer seam but is not in reviewerDispatchSites — name the verbs it serves and give each a DispatchesReviewer predicate, so an agent-facing caller hands the call to a detached run", f)
			continue
		}
		for _, name := range verbs {
			v := Get(name)
			if v == nil {
				t.Errorf("%s lists verb %q, which is not registered", f, name)
				continue
			}
			if v.DispatchesReviewer == nil {
				t.Errorf("verb %q reaches a reviewer seam from %s but declares no DispatchesReviewer", name, f)
			}
		}
	}
	var stale []string
	for f := range reviewerDispatchSites {
		if !found[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("reviewerDispatchSites names files that no longer call a reviewer seam: %v", stale)
	}
}

// A predicate is a function of the request and the wiring: a status change with
// a gate wired dispatches, a title edit does not, and with nothing wired
// nothing does.
func TestDispatchesReviewer_Predicates(t *testing.T) {
	withWiring(t)

	setStatus := json.RawMessage(`{"id":"sty_x","status":"in_progress"}`)
	setTitle := json.RawMessage(`{"id":"sty_x","title":"t"}`)
	setEmpty := json.RawMessage(`{"id":"sty_x","status":""}`)

	transitionGater, createReviewer, amendReviewer = nil, nil, nil
	if DispatchesReviewer("story-set", setStatus) {
		t.Error("story-set dispatches with nothing wired")
	}

	transitionGater = stubGater{}
	if !DispatchesReviewer("story-set", setStatus) || !DispatchesReviewer("task-set", setStatus) {
		t.Error("a status change with a gate wired must dispatch")
	}
	if DispatchesReviewer("story-set", setTitle) || DispatchesReviewer("story-set", setEmpty) {
		t.Error("a set without a status change must not dispatch")
	}
	if DispatchesReviewer("story-get", setStatus) || DispatchesReviewer("no-such-verb", setStatus) {
		t.Error("a verb with no predicate, or an unknown verb, must not dispatch")
	}
	if DispatchesReviewer("story-create", json.RawMessage(`{"title":"t"}`)) {
		t.Error("an ungated create must not dispatch")
	}
	createReviewer = stubCreateReviewer{}
	if !DispatchesReviewer("story-create", json.RawMessage(`{"title":"t"}`)) {
		t.Error("gate_create must dispatch")
	}
	amendReviewer = stubAmendReviewer{}
	if !DispatchesReviewer("story-amend", json.RawMessage(`{"id":"sty_x"}`)) {
		t.Error("an amendment with an amend gate wired must dispatch")
	}
}
