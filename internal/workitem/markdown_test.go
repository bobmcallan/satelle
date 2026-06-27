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
