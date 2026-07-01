package workitem

import (
	"strings"
	"testing"
	"time"
)

func TestMarshalParseRoundTrip(t *testing.T) {
	it := Item{
		ID:                 "sty_abc12345",
		Kind:               KindStory,
		Title:              "Do the thing",
		Body:               "What done looks like.\n\nMore detail.",
		Status:             "in_progress",
		Priority:           "high",
		Category:           "improvement",
		ParentID:           "sty_parent01",
		AcceptanceCriteria: "1. first testable criterion\n2. second criterion",
		Tags:               []string{"area:web", "ui"},
		CreatedAt:          time.Date(2026, 6, 27, 7, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 6, 27, 8, 30, 0, 0, time.UTC),
	}
	got, err := Parse(Marshal(it))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.ID != it.ID || got.Kind != it.Kind || got.Title != it.Title ||
		got.Body != it.Body || got.Status != it.Status || got.Priority != it.Priority ||
		got.Category != it.Category || got.ParentID != it.ParentID ||
		got.AcceptanceCriteria != it.AcceptanceCriteria {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, it)
	}
	if strings.Join(got.Tags, ",") != strings.Join(it.Tags, ",") {
		t.Errorf("tags = %v, want %v", got.Tags, it.Tags)
	}
	if !got.CreatedAt.Equal(it.CreatedAt) || !got.UpdatedAt.Equal(it.UpdatedAt) {
		t.Errorf("times = %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, it.CreatedAt, it.UpdatedAt)
	}
}

func TestParseRejectsNonFrontmatter(t *testing.T) {
	if _, err := Parse([]byte("# Just a title\n\nno frontmatter")); err == nil {
		t.Error("expected an error parsing markdown without frontmatter")
	}
}

// TestMarshalEmitsOKFType asserts the marshaller writes the OKF `type:`
// discriminator (not the legacy `kind:`) for the kind (sty_ef08ce2a).
func TestMarshalEmitsOKFType(t *testing.T) {
	md := string(Marshal(Item{ID: "tsk_1", Kind: KindTask, Title: "T", Status: "backlog"}))
	if !strings.Contains(md, "type: task") {
		t.Errorf("Marshal should emit `type: task`:\n%s", md)
	}
	if strings.Contains(md, "kind:") {
		t.Errorf("Marshal should not emit legacy `kind:`:\n%s", md)
	}
}

// TestParseAcceptsLegacyKind proves Parse still reads the legacy `kind:` key so
// pre-conversion task files keep ingesting (back-compat, sty_ef08ce2a).
func TestParseAcceptsLegacyKind(t *testing.T) {
	legacy := "---\nid: tsk_1\nkind: task\nstatus: backlog\n---\n\n# T\n\nbody"
	it, err := Parse([]byte(legacy))
	if err != nil {
		t.Fatalf("Parse legacy: %v", err)
	}
	if it.Kind != KindTask {
		t.Errorf("legacy kind: not read as KindTask, got %q", it.Kind)
	}
}

// TestExecutionRoundTrip round-trips an execution item — the new kind (with an
// exe_ id) whose parentage points at its task (sty_ef08ce2a).
func TestExecutionRoundTrip(t *testing.T) {
	it := Item{ID: "exe_abc12345", Kind: KindExecution, Title: "run", Status: "in_progress", ParentID: "tsk_parent01"}
	got, err := Parse(Marshal(it))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Kind != KindExecution || got.ParentID != it.ParentID {
		t.Errorf("execution round-trip mismatch: got kind=%q parent=%q", got.Kind, got.ParentID)
	}
}
