package docindex

import (
	"strings"
	"testing"
	"time"
)

func TestOKFConformance(t *testing.T) {
	cases := []struct {
		name, body string
		ok         bool
	}{
		{"events", "---\ntype: Table\n---\n\n# Events", true},
		{"events", "# Events\n\nno frontmatter", false},
		{"events", "---\ntitle: x\n---\n\nbody", false}, // frontmatter but no type
		{"index", "# Documents\n\n* [a](a.md)", true},   // reserved, exempt
		{"log", "## 2026-06-29\n* Creation", true},      // reserved, exempt
	}
	for _, c := range cases {
		err := OKFConformance(c.name, c.body)
		if (err == nil) != c.ok {
			t.Errorf("OKFConformance(%q) ok=%v, want %v (err=%v)", c.name, err == nil, c.ok, err)
		}
	}
}

func TestNormalizeOKF_AddsFrontmatter(t *testing.T) {
	body := "# Commit summary — sty_abc123\n\n- **Commit:** `deadbeef`\n"
	mod := time.Date(2026, 6, 29, 8, 4, 1, 0, time.UTC)
	out, changed := normalizeOKF("commit-summary-sty_abc123", body, mod)
	if !changed {
		t.Fatal("a frontmatter-less doc should be normalized")
	}
	// Conformant and carrying the recommended fields.
	if err := OKFConformance("commit-summary-sty_abc123", out); err != nil {
		t.Errorf("normalized doc not conformant: %v", err)
	}
	for _, want := range []string{
		"type: commit-summary",
		"title: Commit summary — sty_abc123",
		"timestamp: '2026-06-29T08:04:01Z'",
		"tags:\n- commit-summary",
		// no plain-prose line → description falls back to the title.
		"description: Commit summary — sty_abc123",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("normalized frontmatter missing %q\n---\n%s", want, out)
		}
	}
	// Body is preserved after the frontmatter.
	if !strings.Contains(out, "- **Commit:** `deadbeef`") {
		t.Errorf("body not preserved:\n%s", out)
	}
	// Idempotent: a second pass makes no change.
	if out2, changed2 := normalizeOKF("commit-summary-sty_abc123", out, mod); changed2 || out2 != out {
		t.Errorf("normalizeOKF not idempotent")
	}
}

func TestDeriveDescription_SkipsNonProse(t *testing.T) {
	// A commit-summary shape (headings, bullets, numbered ACs, a code fence) has
	// no plain prose line, so description derivation yields "" → caller falls back
	// to the title rather than grabbing a bullet or a numbered criterion.
	body := "# Commit summary\n\n- **Commit:** `abc`\n\n## Acceptance criteria\n\n1. First criterion text.\n2. Second.\n\n## Files\n\n```\na.go | 1 +\n```\n"
	if d := deriveDescription(body); d != "" {
		t.Errorf("deriveDescription on an all-structure body = %q, want \"\"", d)
	}
	// A genuine prose paragraph IS picked up.
	if d := deriveDescription("# Heading\n\nThis is the summary sentence.\n"); d != "This is the summary sentence." {
		t.Errorf("deriveDescription prose = %q", d)
	}
}

func TestNormalizeOKF_PreservesExistingType(t *testing.T) {
	body := "---\ntype: Playbook\ntitle: Keep me\n---\n\n# Keep me"
	out, changed := normalizeOKF("playbook", body, time.Now())
	if changed || out != body {
		t.Errorf("a doc with a non-empty type must be left untouched, got changed=%v", changed)
	}
}

func TestNormalizeOKF_InjectsTypeAndPreservesScalars(t *testing.T) {
	// A typeless attachment (story/name frontmatter) gains a type while its other
	// scalar keys survive.
	body := "---\nstory: sty_x\nname: plan\n---\n\n# Plan body"
	out, changed := normalizeOKF("plan", body, time.Now())
	if !changed {
		t.Fatal("a typeless doc should be normalized")
	}
	if err := OKFConformance("plan", out); err != nil {
		t.Errorf("not conformant after inject: %v", err)
	}
	if !strings.Contains(out, "story: sty_x") || !strings.Contains(out, "name: plan") {
		t.Errorf("existing scalar keys not preserved:\n%s", out)
	}
}

func TestNormalizeOKF_ReservedSkipped(t *testing.T) {
	for _, name := range []string{"index", "log"} {
		body := "# " + name + "\n\nno frontmatter"
		if out, changed := normalizeOKF(name, body, time.Now()); changed || out != body {
			t.Errorf("reserved %s.md must not be normalized", name)
		}
	}
}
