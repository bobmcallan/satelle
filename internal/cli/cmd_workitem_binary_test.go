package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStoryBinaryAttachDocOut (sty_40e5a305 AC1/AC6): CLI binary attach,
// docs listing, doc without --out errors cleanly, doc --out writes bytes.
func TestStoryBinaryAttachDocOut(t *testing.T) {
	_ = tempRepo(t)

	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54,
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
	}
	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, png, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "story", "create",
		"--title", "Binary CLI",
		"--body", "Attach a PNG via CLI.",
		"--acceptance", "1. round trip",
	)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", out)
	}

	if out, err := runRoot(t, "story", "attach", id,
		"--name", "shot.png", "--type", "screenshot",
		"--binary-file", src,
	); err != nil {
		t.Fatalf("attach binary: %v\n%s", err, out)
	}

	// Markdown attach + binary list coexistence is covered at the verb layer
	// (TestBinaryAttachPNGAndPDF). Here we only exercise the binary CLI path —
	// the registered command tree reuses flag state across runRoot calls, so a
	// second attach mode in the same process can trip leftover Changed bits.
	docsOut, err := runRoot(t, "story", "docs", id)
	if err != nil {
		t.Fatalf("docs: %v\n%s", err, docsOut)
	}
	if !strings.Contains(docsOut, "shot.png") || !strings.Contains(docsOut, `"binary": true`) {
		t.Fatalf("docs should list binary distinguishably:\n%s", docsOut)
	}

	// doc without --out must fail and not dump binary to stdout
	noOut, err := runRoot(t, "story", "doc", id, "shot.png")
	if err == nil {
		t.Fatalf("doc without --out should fail, got:\n%s", noOut)
	}
	if strings.Contains(noOut, "\x00") || bytes.Contains([]byte(noOut), png[:8]) {
		t.Errorf("stdout must not carry raw PNG bytes:\n%q", noOut)
	}
	if !strings.Contains(err.Error()+noOut, "binary") && !strings.Contains(err.Error()+noOut, "--out") {
		t.Errorf("error should guide to --out: err=%v out=%s", err, noOut)
	}

	dest := filepath.Join(t.TempDir(), "out.png")
	writeOut, err := runRoot(t, "story", "doc", id, "shot.png", "--out", dest)
	if err != nil {
		t.Fatalf("doc --out: %v\n%s", err, writeOut)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, png) {
		t.Fatalf("written file not byte-identical (got %d want %d)", len(got), len(png))
	}

	// --out without --force refuses overwrite
	if _, err := runRoot(t, "story", "doc", id, "shot.png", "--out", dest); err == nil {
		t.Fatal("overwrite without --force should fail")
	}
}
