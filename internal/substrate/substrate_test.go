package substrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
)

func TestClassifyTable(t *testing.T) {
	embBody := "---\nname: x\n---\n\n# X\n\nbody\n"
	emb := map[string]string{"skills\x00x": embBody}

	// Embedded-only virtual.
	if got := Classify(docindex.Doc{Kind: "skills", Name: "x", Path: "embedded:skills/x.md", Embedded: true}, emb); got != "default" {
		t.Errorf("embedded-only: got %q", got)
	}

	// Stamped unedited seed on disk.
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	stamped := ApplyStamp(embBody, SHA(embBody))
	if err := os.WriteFile(path, []byte(stamped), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Classify(docindex.Doc{Kind: "skills", Name: "x", Path: path}, emb); got != "default" {
		t.Errorf("unedited seed: got %q", got)
	}

	// Edited disk copy.
	edited := ApplyStamp("---\nname: x\n---\n\n# X\n\nedited body\n", "deadbeef")
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Classify(docindex.Doc{Kind: "skills", Name: "x", Path: path}, emb); got != "edited" {
		t.Errorf("edited: got %q", got)
	}

	// No embedded counterpart.
	if got := Classify(docindex.Doc{Kind: "skills", Name: "local", Path: path}, emb); got != "authored" {
		t.Errorf("authored: got %q", got)
	}
}

func TestStampRoundTrip(t *testing.T) {
	body := "---\nname: a\n---\n\nbody\n"
	sha := SHA(body)
	stamped := ApplyStamp(body, sha)
	stripped, got, ok := StripStamp(stamped)
	if !ok || got != sha {
		t.Fatalf("strip: ok=%v stamp=%q", ok, got)
	}
	if stripped != body {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", stripped, body)
	}
}
