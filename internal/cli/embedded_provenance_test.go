package cli

import (
	"io"
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
	verb, bres, err := reconcileEmbeddedFile(dataDir, relPath, embeddedBody)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return verb, bres.LocalPath, dataDir
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
	pre := applyEmbeddedStamp(oldBody, embeddedSHA(oldBody))
	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", newBody)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUpdated {
		t.Fatalf("verb=%q, want updated", verb)
	}
	// sty_873a5380: converge-overwrite backs up the pre-image first.
	if bres.LocalPath == "" || !strings.Contains(filepath.ToSlash(bres.LocalPath), "backups/pre-mutation/principles/sample.md") {
		t.Fatalf("bres.LocalPath=%q, want under backups/pre-mutation/", bres.LocalPath)
	}
	if b, err := os.ReadFile(bres.LocalPath); err != nil || string(b) != pre {
		t.Errorf("pre-mutation bres.LocalPath missing or wrong content: err=%v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != applyEmbeddedStamp(newBody, embeddedSHA(newBody)) {
		t.Errorf("file not converged to current embedded")
	}
}

// TestReconcileNoOp (AC3): a copy byte-identical to the current stamped embedded
// is a no-op — verb unchanged, bytes untouched, no bres.LocalPath.
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
	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dest)
	if verb != reconcileUnchanged || bres.LocalPath != "" || string(before) != string(after) {
		t.Fatalf("verb=%q bres.LocalPath=%q changed=%v, want unchanged no-op", verb, bres.LocalPath, string(before) != string(after))
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
	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileDiverged {
		t.Fatalf("verb = %q, want diverged", verb)
	}
	if got, _ := os.ReadFile(dest); string(got) != edited {
		t.Error("diverged: original was modified (must be left intact)")
	}
	if b, err := os.ReadFile(bres.LocalPath); err != nil || string(b) != edited {
		t.Errorf("bres.LocalPath missing or wrong content at %s: err=%v", bres.LocalPath, err)
	}
	if !strings.Contains(filepath.ToSlash(bres.LocalPath), "backups/diverged/principles/sample.md") {
		t.Errorf("bres.LocalPath path = %s, want under backups/diverged/principles/", bres.LocalPath)
	}
}

// TestDivergedAdvisoryPrintsTheRealBackupPath (sty_338a53f8): the diverged line
// must name the path the pre-image was ACTUALLY written to. Backups land under
// the backup ROOT, which in a real run is the home-keyed runtime dir — so the
// composed `.satelle/backups/…` this line used to print named a path that did
// not exist and sent the operator hunting in the wrong tree.
func TestDivergedAdvisoryPrintsTheRealBackupPath(t *testing.T) {
	dataDir := t.TempDir()
	// A backup root DISTINCT from dataDir — the shape of a home-keyed runtime dir.
	runtimeDir := t.TempDir()

	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(applyEmbeddedStamp(sampleFM, embeddedSHA(sampleFM)), "body line", "operator edit", 1)
	if err := os.WriteFile(dest, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM,
		BackupOpts{BackupsDir: runtimeDir, LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileDiverged {
		t.Fatalf("verb = %q, want diverged", verb)
	}

	line := reconcileReportLine(verb, "principles/sample.md", bres.LocalPath)
	if !strings.Contains(line, bres.LocalPath) {
		t.Errorf("advisory %q must name the real backup path %q", line, bres.LocalPath)
	}
	// The printed path exists on disk — the whole point of the finding.
	for _, field := range strings.Fields(line) {
		field = strings.Trim(field, ";")
		if !strings.HasPrefix(field, runtimeDir) {
			continue
		}
		if _, serr := os.Stat(field); serr != nil {
			t.Errorf("advisory names %q, which does not exist: %v", field, serr)
		}
		return
	}
	t.Errorf("advisory %q names no path under the backup root %q", line, runtimeDir)
}

// TestReconcileReportLineFallsBackWhenNoBackup: a caller with no BackupResult
// still gets a sane line rather than a dangling "backed up to ".
func TestReconcileReportLineFallsBackWhenNoBackup(t *testing.T) {
	line := reconcileReportLine(reconcileDiverged, "principles/sample.md", "")
	if !strings.Contains(line, "backups/diverged/principles/sample.md") {
		t.Errorf("empty backup path must fall back to the composed form, got %q", line)
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
	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUnchanged || bres.LocalPath != "" {
		t.Fatalf("verb=%q bres.LocalPath=%q, want unchanged/no-bres.LocalPath", verb, bres.LocalPath)
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

// TestInitStampsAndValidates (sty_29e5a9a5): a fresh init validates green without
// seeding defaults; substrate edit materializes a stamped principle for day-one edit.
func TestInitStampsAndValidates(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	body, ok := embeddedDefault("principles", "satelle-agent-goals")
	if !ok {
		t.Skip("satelle-agent-goals embedded default not present")
	}
	// No seed on disk after init.
	seedPath := filepath.Join(repo, ".satelle", "principles", "satelle-agent-goals.md")
	if _, err := os.Stat(seedPath); err == nil {
		t.Fatal("init must not seed principles (virtual defaults)")
	}
	// Materialize-on-edit produces a stamped copy.
	if err := runSubstrateEdit(io.Discard, filepath.Join(repo, ".satelle"), nil, "principles", "satelle-agent-goals"); err != nil {
		t.Fatalf("substrate edit: %v", err)
	}
	raw, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	_, stamp, stamped := stripEmbeddedStamp(string(raw))
	if !stamped || stamp != embeddedSHA(body) {
		t.Errorf("edited principle stamp = %q stamped=%v, want %q", stamp, stamped, embeddedSHA(body))
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

	verb, bres, err := reconcileEmbeddedFile(dataDir, rel, newBody)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUpdated {
		t.Fatalf("verb=%q, want updated", verb)
	}
	if bres.LocalPath == "" || !strings.Contains(filepath.ToSlash(bres.LocalPath), "backups/pre-mutation/") {
		t.Fatalf("bres.LocalPath=%q, want pre-mutation path", bres.LocalPath)
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

// TestReconcileRestampIdentical (sty_a9ec33e7): stampless body byte-identical
// to the embedded default is re-stamped in place (verb restamped).
func TestReconcileRestampIdentical(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	// stampless identical body
	if err := os.WriteFile(dest, []byte(sampleFM), 0o644); err != nil {
		t.Fatal(err)
	}
	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileRestamped {
		t.Fatalf("verb=%q, want restamped", verb)
	}
	got, _ := os.ReadFile(dest)
	want := applyEmbeddedStamp(sampleFM, embeddedSHA(sampleFM))
	if string(got) != want {
		t.Errorf("restamp did not write stamped body")
	}
	if bres.LocalPath == "" {
		t.Error("restamp must back up pre-image first")
	}
}

// TestReconcileStamplessEditedUntouched: stampless different body stays put.
func TestReconcileStamplessEditedUntouched(t *testing.T) {
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "principles", "sample.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	authored := "---\nname: sample\ntype: principle\n---\n\n# Different\n"
	if err := os.WriteFile(dest, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	verb, bres, err := reconcileEmbeddedFile(dataDir, "principles/sample.md", sampleFM)
	if err != nil {
		t.Fatal(err)
	}
	if verb != reconcileUnchanged || bres.LocalPath != "" {
		t.Fatalf("verb=%q backup=%q, want unchanged", verb, bres.LocalPath)
	}
	if got, _ := os.ReadFile(dest); string(got) != authored {
		t.Error("edited stampless file was modified")
	}
}
