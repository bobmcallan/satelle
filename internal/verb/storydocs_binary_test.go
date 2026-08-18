package verb_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// minimalPNG is a tiny valid 1×1 PNG (includes a \x00 and the sequence that
// would look like frontmatter if text-handled).
func minimalPNG() []byte {
	// 1x1 transparent PNG
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // signature
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR len+type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, // IDAT
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, // IEND
		0xae, 0x42, 0x60, 0x82,
		// plant frontmatter-looking bytes that must survive byte-exact
		0x0a, 0x2d, 0x2d, 0x2d, 0x0a, 0x00,
	}
}

func minimalPDF() []byte {
	return []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")
}

func wireDocs(t *testing.T) (storyDir string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	storyDir = filepath.Join(dir, "stories")
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetStoryDir(storyDir)
	verb.SetAttachmentPolicy(0, nil) // defaults
	t.Cleanup(func() {
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetTxRunner(nil)
		verb.SetDocIndexStore(nil)
		verb.SetStoryDir("")
		verb.SetAttachmentPolicy(0, nil)
	})
	return storyDir
}

func createStory(t *testing.T) workitem.Item {
	t.Helper()
	var st workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "Binary docs", "acceptance_criteria": "1. ok",
	}), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestMarkdownAttachGolden (AC2): byte-for-byte identical markdown attach
// output including frontmatter — tripwire for baseSafe extraction.
func TestMarkdownAttachGolden(t *testing.T) {
	storyDir := wireDocs(t)
	st := createStory(t)
	call(t, "story-doc-attach", map[string]any{
		"story_id": st.ID, "name": "note", "type": "note",
		"body": "hello body",
	})
	got, err := os.ReadFile(filepath.Join(storyDir, st.ID, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nstory: " + st.ID + "\ntype: note\nname: note\n---\n\nhello body\n"
	if string(got) != want {
		t.Fatalf("markdown path mutated:\n got %q\nwant %q", got, want)
	}
}

// TestBinaryAttachPNGAndPDF (AC1, AC3, AC10): PNG + PDF round-trip, sidecar,
// ledger evidence.
func TestBinaryAttachPNGAndPDF(t *testing.T) {
	storyDir := wireDocs(t)
	st := createStory(t)

	png := minimalPNG()
	pdf := minimalPDF()

	var pngRef struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		ContentType string `json:"content_type"`
		SHA256      string `json:"sha256"`
		Size        int64  `json:"size"`
		Binary      bool   `json:"binary"`
	}
	if err := json.Unmarshal(call(t, "story-doc-attach-binary", map[string]any{
		"story_id": st.ID, "name": "shot.png", "type": "screenshot",
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(png),
	}), &pngRef); err != nil {
		t.Fatal(err)
	}
	if pngRef.Name != "shot.png" || !pngRef.Binary || pngRef.ContentType != "image/png" {
		t.Fatalf("png attach ref: %+v", pngRef)
	}
	if pngRef.Size != int64(len(png)) {
		t.Fatalf("size = %d, want %d", pngRef.Size, len(png))
	}
	sum := sha256.Sum256(png)
	wantDigest := hex.EncodeToString(sum[:])
	if pngRef.SHA256 != wantDigest {
		t.Fatalf("sha256 = %s, want %s", pngRef.SHA256, wantDigest)
	}

	// On-disk bytes identical; no frontmatter prefix.
	gotPNG, err := os.ReadFile(filepath.Join(storyDir, st.ID, "shot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPNG, png) {
		t.Fatalf("png on disk not byte-identical (len got=%d want=%d)", len(gotPNG), len(png))
	}
	if bytes.HasPrefix(gotPNG, []byte("---")) {
		t.Fatal("png must not have frontmatter prepended")
	}

	// Sidecar
	scRaw, err := os.ReadFile(filepath.Join(storyDir, st.ID, "shot.png.satelle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sc map[string]any
	if err := json.Unmarshal(scRaw, &sc); err != nil {
		t.Fatal(err)
	}
	if sc["sha256"] != wantDigest || sc["binary"] != true {
		t.Fatalf("sidecar: %s", scRaw)
	}

	// PDF
	call(t, "story-doc-attach-binary", map[string]any{
		"story_id": st.ID, "name": "spec.pdf", "type": "reference",
		"content_type": "application/pdf",
		"data_base64":  base64.StdEncoding.EncodeToString(pdf),
	})
	gotPDF, err := os.ReadFile(filepath.Join(storyDir, st.ID, "spec.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPDF, pdf) {
		t.Fatal("pdf on disk not byte-identical")
	}

	// Get binary returns base64 of original
	var got struct {
		DataB64 string `json:"data_base64"`
		Binary  bool   `json:"binary"`
	}
	if err := json.Unmarshal(call(t, "story-doc-get-binary", map[string]any{
		"story_id": st.ID, "name": "shot.png",
	}), &got); err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(got.DataB64)
	if !bytes.Equal(decoded, png) {
		t.Fatal("get-binary round-trip mismatch")
	}

	// Markdown get on binary errors with guidance
	_, getErr := verb.Dispatch(context.Background(), "story-doc-get", mustJSON(t, map[string]any{
		"story_id": st.ID, "name": "shot.png",
	}))
	if getErr == nil || !strings.Contains(getErr.Error(), "binary attachment") {
		t.Fatalf("markdown get on binary: want binary error, got %v", getErr)
	}
	if !strings.Contains(getErr.Error(), "--out") {
		t.Fatalf("error should mention --out: %v", getErr)
	}

	// List distinguishes binary
	var docs []struct {
		Name   string `json:"name"`
		Binary bool   `json:"binary"`
		Type   string `json:"type"`
	}
	json.Unmarshal(call(t, "story-doc-list", map[string]any{"story_id": st.ID}), &docs)
	var sawPNG, sawPDF bool
	for _, d := range docs {
		if d.Name == "shot.png" {
			sawPNG = d.Binary
		}
		if d.Name == "spec.pdf" {
			sawPDF = d.Binary
		}
	}
	if !sawPNG || !sawPDF {
		t.Fatalf("list missing binaries: %+v", docs)
	}

	// Ledger (AC10)
	var entries []map[string]any
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": st.ID, "limit": 100}), &entries)
	var sawLedger bool
	for _, e := range entries {
		if e["kind"] != verb.KindStoryDocAttached {
			continue
		}
		body, _ := e["body"].(string)
		if strings.Contains(body, "shot.png") &&
			strings.Contains(body, wantDigest) &&
			strings.Contains(body, "bytes") {
			sawLedger = true
		}
	}
	if !sawLedger {
		t.Fatalf("ledger missing binary attach with hash; entries=%v", entries)
	}
}

// TestBinarySafeNameTraversal (AC1): path traversal and reserved names rejected.
func TestBinarySafeNameTraversal(t *testing.T) {
	storyDir := wireDocs(t)
	st := createStory(t)
	png := minimalPNG()
	b64 := base64.StdEncoding.EncodeToString(png)

	cases := []string{
		"../x.png",
		"/etc/passwd",
		"a/b.png",
		"..",
		"",
		"x.satelle.json",
		".hidden.png",
		"noext",
		"notes.md", // markdown namespace reserved — must not poison ItemDocs
		"plan.md",
	}
	for _, name := range cases {
		_, err := verb.Dispatch(context.Background(), "story-doc-attach-binary", mustJSON(t, map[string]any{
			"story_id": st.ID, "name": name, "type": "screenshot",
			"content_type": "image/png", "data_base64": b64,
		}))
		if err == nil {
			t.Errorf("name %q: want rejection", name)
		}
	}
	// Nothing escaped the story dir.
	entries, _ := os.ReadDir(storyDir)
	for _, e := range entries {
		if e.Name() != st.ID {
			t.Errorf("unexpected entry outside story: %s", e.Name())
		}
	}
	// Story dir should have no files (all rejects).
	sub, _ := os.ReadDir(filepath.Join(storyDir, st.ID))
	for _, e := range sub {
		if strings.HasSuffix(e.Name(), ".png") || strings.HasSuffix(e.Name(), verb.SidecarSuffix) {
			t.Errorf("reject left residue: %s", e.Name())
		}
	}
}

// TestBinaryCapAndAllowlist (AC7).
func TestBinaryCapAndAllowlist(t *testing.T) {
	storyDir := wireDocs(t)
	st := createStory(t)

	// Lower the cap for the over-cap case.
	verb.SetAttachmentPolicy(100, nil)
	big := make([]byte, 0, 200)
	big = append(big, minimalPNG()...)
	for len(big) < 150 {
		big = append(big, 0x00)
	}
	// Re-sign as non-PNG once padded — DetectContentType may still see PNG prefix.
	// Cap check is on length first conceptually; our code checks length before type.
	_, err := verb.Dispatch(context.Background(), "story-doc-attach-binary", mustJSON(t, map[string]any{
		"story_id": st.ID, "name": "big.png", "type": "screenshot",
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(big),
	}))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("over-cap: want too large, got %v", err)
	}
	// No residue
	if _, err := os.Stat(filepath.Join(storyDir, st.ID, "big.png")); !os.IsNotExist(err) {
		t.Error("over-cap left a file")
	}
	if _, err := os.Stat(filepath.Join(storyDir, st.ID, "big.png"+verb.SidecarSuffix)); !os.IsNotExist(err) {
		t.Error("over-cap left a sidecar")
	}

	// Restore default cap; SVG denied.
	verb.SetAttachmentPolicy(0, nil)
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	_, err = verb.Dispatch(context.Background(), "story-doc-attach-binary", mustJSON(t, map[string]any{
		"story_id": st.ID, "name": "icon.svg", "type": "screenshot",
		"content_type": "image/svg+xml",
		"data_base64":  base64.StdEncoding.EncodeToString(svg),
	}))
	if err == nil || !strings.Contains(err.Error(), "unsupported_type") {
		t.Fatalf("svg: want unsupported_type, got %v", err)
	}
	// HTML renamed to .png — sniff catches it.
	html := []byte("<!DOCTYPE html><html><body>x</body></html>")
	_, err = verb.Dispatch(context.Background(), "story-doc-attach-binary", mustJSON(t, map[string]any{
		"story_id": st.ID, "name": "shot.png", "type": "screenshot",
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(html),
	}))
	if err == nil {
		t.Fatal("html-as-png: want rejection")
	}

	// Allowed PNG at small size succeeds under a raised-enough cap.
	verb.SetAttachmentPolicy(0, nil)
	png := minimalPNG()
	call(t, "story-doc-attach-binary", map[string]any{
		"story_id": st.ID, "name": "ok.png", "type": "screenshot",
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(png),
	})
	if _, err := os.Stat(filepath.Join(storyDir, st.ID, "ok.png")); err != nil {
		t.Fatalf("allowed png missing: %v", err)
	}
}

// TestBinaryRejectsMarkdownNamespace (sty_40e5a305 AC4/AC5 fix): an
// application/octet-stream named *.md must be rejected so ItemDocs never
// inlines raw bytes under the markdown walk.
func TestBinaryRejectsMarkdownNamespace(t *testing.T) {
	wireDocs(t)
	st := createStory(t)
	blob := make([]byte, 200)
	for i := range blob {
		blob[i] = 0xab
	}
	_, err := verb.Dispatch(context.Background(), "story-doc-attach-binary", mustJSON(t, map[string]any{
		"story_id": st.ID, "name": "notes.md", "type": "blob",
		"content_type": "application/octet-stream",
		"data_base64":  base64.StdEncoding.EncodeToString(blob),
	}))
	if err == nil {
		t.Fatal("binary named notes.md must be rejected")
	}
	docs, err := verb.ItemDocs(context.Background(), st.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.Name == "notes" && len(d.Body) > 0 && !d.Binary {
			t.Fatalf("unexpected md body in ItemDocs: %+v", d)
		}
	}
}
