package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-proof",
		Description: "Enumerate tests added or changed since a story's engagement baseline (enumeration only)",
		Invoke:      storyProof,
	})
}

type storyProofReq struct {
	ID string `json:"id"`
}

// StoryProofResult is report-only: paths and, when parseable, test function
// names. No pass/fail field. story-proof never runs tests (sty_76796b8e).
type StoryProofResult struct {
	StoryID      string           `json:"story_id"`
	Baseline     string           `json:"baseline,omitempty"`
	Head         string           `json:"head,omitempty"`
	Dirty        bool             `json:"dirty"`
	State        string           `json:"state"`
	Tests        []StoryProofTest `json:"tests"`
	Skipped      []StoryProofSkip `json:"skipped"`
	NonTestFiles []string         `json:"non_test_files"`
	Note         string           `json:"note"`
}

// StoryProofTest is one classified test file from the engagement slice.
type StoryProofTest struct {
	Path      string   `json:"path"`
	Language  string   `json:"language"`
	Functions []string `json:"functions,omitempty"`
}

// StoryProofSkip is a test-shaped path we could not parse.
type StoryProofSkip struct {
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	Reason   string `json:"reason"`
}

func storyProof(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req storyProofReq
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		var wrap struct {
			Story struct {
				ID string `json:"id"`
			} `json:"story"`
			StoryID string `json:"story_id"`
			ID      string `json:"id"`
		}
		if err := json.Unmarshal(raw, &wrap); err == nil {
			req.ID = wrap.ID
			if req.ID == "" {
				req.ID = wrap.StoryID
			}
			if req.ID == "" {
				req.ID = wrap.Story.ID
			}
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("story-proof: id required (pass <id> or pipe transition payload with story.id on stdin)")
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if it.Kind != workitem.KindStory {
		return nil, fmt.Errorf("story-proof: %s is not a story", req.ID)
	}

	out := StoryProofResult{
		StoryID:      it.ID,
		Tests:        []StoryProofTest{},
		Skipped:      []StoryProofSkip{},
		NonTestFiles: []string{},
		Note:         "enumeration only — no pass/fail; does not run tests; gates decide",
	}

	sl, err := resolveEngagementSlice(ctx, it, false)
	if err != nil {
		switch {
		case errors.Is(err, errNoBaseline):
			out.State = "no_baseline"
			out.Note = err.Error()
		case errors.Is(err, errEmptyHead):
			// Distinct from never-engaged: git was unavailable at engage.
			out.State = "no_baseline"
			out.Note = err.Error()
		case errors.Is(err, errForeignTree):
			out.State = "foreign_tree"
			out.Note = err.Error()
		case errors.Is(err, errNoGit):
			out.State = "no_git"
			out.Note = err.Error()
		default:
			out.State = "no_git"
			out.Note = err.Error()
		}
		return json.Marshal(out)
	}

	head, dirty, _ := gitHeadAndDirty(sl.Dir)
	out.Baseline = sl.SinceSHA
	out.Head = head
	out.Dirty = dirty
	out.State = "ok"

	for _, p := range sl.Files {
		lang, isTest := classifyTestFile(p)
		if !isTest {
			out.NonTestFiles = append(out.NonTestFiles, p)
			continue
		}
		if lang != "go" {
			out.Skipped = append(out.Skipped, StoryProofSkip{
				Path: p, Language: lang, Reason: "no parser for " + lang,
			})
			continue
		}
		full := filepath.Join(sl.Dir, p)
		body, rerr := os.ReadFile(full)
		if rerr != nil {
			out.Skipped = append(out.Skipped, StoryProofSkip{
				Path: p, Language: lang, Reason: "file not present in worktree",
			})
			continue
		}
		out.Tests = append(out.Tests, StoryProofTest{
			Path:      p,
			Language:  "go",
			Functions: parseGoTestFuncs(string(body)),
		})
	}
	return json.Marshal(out)
}

func classifyTestFile(path string) (language string, isTest bool) {
	base := filepath.Base(path)
	slash := filepath.ToSlash(path)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return "go", true
	case strings.HasSuffix(base, "_test.py") || (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")):
		return "python", true
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return "javascript", true
	case strings.HasSuffix(base, ".rs") && (strings.Contains(slash, "/tests/") || strings.HasPrefix(slash, "tests/")):
		return "rust", true
	case hasTestPathSegment(slash):
		return "unknown", true
	default:
		return "", false
	}
}

func hasTestPathSegment(slash string) bool {
	for _, seg := range strings.Split(slash, "/") {
		switch seg {
		case "test", "tests", "spec", "specs":
			return true
		}
	}
	return false
}

var goTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

func parseGoTestFuncs(src string) []string {
	matches := goTestFunc.FindAllStringSubmatch(src, -1)
	if matches == nil {
		return []string{}
	}
	out := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}
