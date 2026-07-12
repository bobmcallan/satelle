package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// sampleFM is a minimal frontmatter document used as a stand-in embedded body.
const sampleFM = "---\nname: sample\ntype: principle\ndescription: d\n---\n\n# Sample\n\nbody line\n"

// TestEmbeddedStampRoundTrip pins the exact byte-inverse invariant AC6 rests on:
// stripEmbeddedStamp(applyEmbeddedStamp(b, s)) reproduces b and recovers s.
func TestEmbeddedStampRoundTrip(t *testing.T) {
	sha := embeddedSHA(sampleFM)
	stamped := applyEmbeddedStamp(sampleFM, sha)
	if !strings.Contains(stamped, embeddedStampKey+": "+sha) {
		t.Fatalf("stamp not inserted: %q", stamped)
	}
	back, got, ok := stripEmbeddedStamp(stamped)
	if !ok || got != sha {
		t.Fatalf("strip: ok=%v stamp=%q want %q", ok, got, sha)
	}
	if back != sampleFM {
		t.Fatalf("round-trip not byte-exact:\n got %q\nwant %q", back, sampleFM)
	}
}

// reconcileAt is a test helper: reconcile relPath under a fresh temp dataDir with
// the given embedded body, returning the verb and the dataDir.
func reconcileAt(t *testing.T, relPath, embeddedBody string) (reconcileVerb, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	verb, backup, err := reconcileEmbeddedFile(dataDir, relPath, embeddedBody)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return verb, backup, dataDir
}

// TestReconcileFreshCreate (AC1): an absent file is created carrying a stamp equal
// to sha256 of the embedded body, and the stripped body equals the embedded body.
func TestReconcileFreshCreate(t *testing.T) {
	verb, _, dataDir := reconcileAt(t, "principles/sample.md", sampleFM)
	if verb != reconcileCreated {
		t.Fatalf("verb = %q, want created", verb)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "principles", "sample.md"))
	if err != nil {
		t.Fatal(err)
	}
	stripped, stamp, ok := stripEmbeddedStamp(string(raw))
	if !ok {
		t.Fatal("created file carries no embedded_sha stamp")
	}
	if stamp != embeddedSHA(sampleFM) {
		t.Errorf("stamp = %q, want %q", stamp, embeddedSHA(sampleFM))
	}
	if stripped != sampleFM {
		t.Errorf("stripped body != embedded body")
	}
}

// TestReconcileUneditedUpgrade (AC2): an UNEDITED copy whose stamp predates a
// changed embedded body is overwritten with the current embedded body + re-stamp.
func TestReconcileUneditedUpgrade(t *testing.T) {
	oldBody := sampleFM
	newBody := strings.Replace(sampleFM, "body line", "body line v2", 1)
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	// seed an unedited OLD stamped copy
	if err := os.WriteFile(dest, []byte(applyEmbeddedStamp(oldBody, embeddedSHA(oldBody))), 0o644); err != nil {
		t.Fatal(err)
	}
	verb, backup, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", newBody)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUpdated || backup != "" {
		t.Fatalf("verb=%q backup=%q, want updated/no-backup", verb, backup)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != applyEmbeddedStamp(newBody, embeddedSHA(newBody)) {
		t.Errorf("file not converged to current embedded")
	}
}

// TestReconcileNoOp (AC3): a copy byte-identical to the current stamped embedded
// is a no-op — verb unchanged, bytes untouched, no backup.
func TestReconcileNoOp(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	current := applyEmbeddedStamp(sampleFM, embeddedSHA(sampleFM))
	if err := os.WriteFile(dest, []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(dest)
	verb, backup, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dest)
	if verb != reconcileUnchanged || backup != "" || string(before) != string(after) {
		t.Fatalf("verb=%q backup=%q changed=%v, want unchanged no-op", verb, backup, string(before) != string(after))
	}
}

// TestReconcileEditedDiverge (AC4): an EDITED copy is not clobbered; it is backed
// up under .satelle/backups/diverged and the original is left in place.
func TestReconcileEditedDiverge(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	// stamped-current, then an operator edit to the body (stamp now stale vs body)
	edited := strings.Replace(applyEmbeddedStamp(sampleFM, embeddedSHA(sampleFM)), "body line", "operator edit", 1)
	if err := os.WriteFile(dest, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	verb, backup, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileDiverged {
		t.Fatalf("verb = %q, want diverged", verb)
	}
	if got, _ := os.ReadFile(dest); string(got) != edited {
		t.Error("diverged: original was modified (must be left intact)")
	}
	if b, err := os.ReadFile(backup); err != nil || string(b) != edited {
		t.Errorf("backup missing or wrong content at %s: err=%v", backup, err)
	}
	if !strings.Contains(filepath.ToSlash(backup), "backups/diverged/principles/sample.md") {
		t.Errorf("backup path = %s, want under backups/diverged/principles/", backup)
	}
}

// TestReconcileUnstampedUntouched (AC5): a file with NO stamp is operator-authored
// and never modified, even when its body differs from the embedded default.
func TestReconcileUnstampedUntouched(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	authored := "---\nname: sample\ntype: principle\n---\n\n# Authored\n\ntotally different\n"
	if err := os.WriteFile(dest, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	verb, backup, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUnchanged || backup != "" {
		t.Fatalf("verb=%q backup=%q, want unchanged/no-backup", verb, backup)
	}
	if got, _ := os.ReadFile(dest); string(got) != authored {
		t.Error("unstamped operator file was modified")
	}
}

// TestReconcileIdempotent (AC6): reconciling a fresh-stamped current file twice is
// a no-op both times — re-stamping never counts as an edit.
func TestReconcileIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if v, _, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM); err != nil || v != reconcileCreated {
		t.Fatalf("first: v=%q err=%v", v, err)
	}
	first, _ := os.ReadFile(dest)
	if v, _, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM); err != nil || v != reconcileUnchanged {
		t.Fatalf("second: v=%q err=%v, want unchanged", v, err)
	}
	second, _ := os.ReadFile(dest)
	if string(first) != string(second) {
		t.Error("second reconcile changed bytes (not idempotent)")
	}
}

// TestEveryEmbeddedDefaultStampable (guard): every shipped embedded default has
// frontmatter, so applyEmbeddedStamp actually inserts a recoverable stamp — the
// precondition that makes the whole mechanism apply to the real substrate.
func TestEveryEmbeddedDefaultStampable(t *testing.T) {
	for _, d := range config.EmbeddedDefaults() {
		stamped := applyEmbeddedStamp(d.Body, embeddedSHA(d.Body))
		stripped, stamp, ok := stripEmbeddedStamp(stamped)
		if !ok {
			t.Errorf("%s/%s: no frontmatter — stamp not applied", d.Kind, d.Name)
			continue
		}
		if stamp != embeddedSHA(d.Body) || stripped != d.Body {
			t.Errorf("%s/%s: stamp round-trip not exact", d.Kind, d.Name)
		}
	}
}

// TestInitStampsAndValidates (AC1 at init level): a fresh init stamps materialised
// embedded files and still validates green.
func TestInitStampsAndValidates(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// A materialised embedded principle carries a stamp matching its embedded body.
	body, ok := embeddedDefault("principles", "satelle-agent-goals")
	if !ok {
		t.Skip("satelle-agent-goals embedded default not present")
	}
	raw, err := os.ReadFile(filepath.Join(repo, ".satelle", "principles", "satelle-agent-goals.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, stamp, stamped := stripEmbeddedStamp(string(raw))
	if !stamped || stamp != embeddedSHA(body) {
		t.Errorf("seeded principle stamp = %q stamped=%v, want %q", stamp, stamped, embeddedSHA(body))
	}
}

// TestReconcileRecogniseBlockageUpgrade pins AC5 for sty_0334d12b: an UNEDITED
// stamped on-disk copy of satelle-recognise-blockage whose stamp predates a
// fixed embedded body is overwritten and re-stamped (order:1 converge).
func TestReconcileRecogniseBlockageUpgrade(t *testing.T) {
	newBody, ok := embeddedDefault("principles", "satelle-recognise-blockage")
	if !ok {
		t.Fatal("satelle-recognise-blockage must be embedded")
	}
	// Simulate a prior seed: same frontmatter shell, older body that still
	// carried the pre-fix "nothing engaged" signal.
	oldBody := "---\nname: satelle-recognise-blockage\nscope: system\ntype: principle\ntags: [type:principle, principles:session]\napplies_to: [\"*\"]\ndescription: old\n---\n\n# Recognise blockage\n\nnothing engaged was blockage (pre-fix seed)\n"
	if !strings.Contains(oldBody, "nothing engaged") {
		t.Fatal("fixture must contain pre-fix phrase")
	}
	if strings.Contains(newBody, "nothing engaged") {
		t.Fatal("current embedded body still contains pre-fix phrase")
	}

	dataDir := t.TempDir()
	rel := "principles/satelle-recognise-blockage.md"
	dest := filepath.Join(dataDir, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(applyEmbeddedStamp(oldBody, embeddedSHA(oldBody))), 0o644); err != nil {
		t.Fatal(err)
	}

	verb, backup, err := reconcileEmbeddedFile(dataDir, rel, newBody)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUpdated || backup != "" {
		t.Fatalf("verb=%q backup=%q, want updated/no-backup", verb, backup)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	want := applyEmbeddedStamp(newBody, embeddedSHA(newBody))
	if string(got) != want {
		t.Errorf("on-disk body did not converge to current embedded+stamp")
	}
	stripped, stamp, stamped := stripEmbeddedStamp(string(got))
	if !stamped || stamp != embeddedSHA(newBody) || stripped != newBody {
		t.Errorf("stamp/body mismatch after upgrade: stamped=%v stamp=%q", stamped, stamp)
	}
}
