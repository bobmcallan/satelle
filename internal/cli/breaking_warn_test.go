package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/verb"
)

// isolateSession pins the session identity: SATELLE_HOME points the published-
// session lookup at an empty dir so an ancestor harness session can never leak in.
func isolateSession(t *testing.T, id string) {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, id)
}

func manualWarning(t *testing.T) *breakingWarning {
	t.Helper()
	bw, err := breakingDrift("0.0.380", "0.0.396", []verb.ChangelogEntry{breakingEntry("0.0.385", "do it by hand.")})
	if err != nil || bw == nil {
		t.Fatalf("fixture must warn: warn=%v err=%v", bw, err)
	}
	return bw
}

// TestWarnBreakingOncePerSession (sty_6e143870 AC2): the warning prints the first
// time a session runs a command and never again in that session; a different
// session, or a newer release, warns again.
func TestWarnBreakingOncePerSession(t *testing.T) {
	rt := t.TempDir()
	bw := manualWarning(t)

	isolateSession(t, "sess-a")
	var first, second bytes.Buffer
	warnBreakingOnce(rt, bw, &first)
	warnBreakingOnce(rt, bw, &second)
	if !strings.Contains(first.String(), "do it by hand.") {
		t.Fatalf("first command in a session must print the remediation:\n%s", first.String())
	}
	if second.Len() != 0 {
		t.Fatalf("second command in the same session must be silent, got:\n%s", second.String())
	}
	if _, err := os.Stat(filepath.Join(rt, breakingWarnedName)); err != nil {
		t.Fatalf("the once-marker must live under the runtime dir: %v", err)
	}

	isolateSession(t, "sess-b")
	var other bytes.Buffer
	warnBreakingOnce(rt, bw, &other)
	if other.Len() == 0 {
		t.Fatal("a new session must be warned again")
	}

	newer := *bw
	newer.Version = "0.0.390"
	var rel bytes.Buffer
	warnBreakingOnce(rt, &newer, &rel)
	if rel.Len() == 0 {
		t.Fatal("a newer Breaking release must warn again within the same session")
	}
}

// TestWarnBreakingOnceNoSessionRepeats: with no session there is nothing to scope
// "once" to, so the warning prints every time rather than going silent.
func TestWarnBreakingOnceNoSessionRepeats(t *testing.T) {
	isolateSession(t, "")
	rt := t.TempDir()
	bw := manualWarning(t)
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		warnBreakingOnce(rt, bw, &out)
		if out.Len() == 0 {
			t.Fatalf("call %d: no session must still warn", i)
		}
	}
	if _, err := os.Stat(filepath.Join(rt, breakingWarnedName)); err == nil {
		t.Error("no session, so no marker should be written")
	}
}

// TestWarnBreakingOnceMarkerFailureNeverFails: an unwritable marker means the
// warning may repeat — it must not panic or swallow the warning.
func TestWarnBreakingOnceMarkerFailureNeverFails(t *testing.T) {
	isolateSession(t, "sess-a")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bw := manualWarning(t)
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		warnBreakingOnce(filepath.Join(blocker, "rt"), bw, &out)
		if out.Len() == 0 {
			t.Fatalf("call %d: an unwritable marker must still warn", i)
		}
	}
	var none bytes.Buffer
	warnBreakingOnce(t.TempDir(), nil, &none)
	if none.Len() != 0 {
		t.Errorf("no warning means no output, got %q", none.String())
	}
}

// TestBreakingFindings (sty_6e143870 AC3): doctor reports an unacknowledged
// Breaking entry — an error when init heals it (commands refuse), a warning when
// the migration is manual — and nothing once the stamp has caught up.
func TestBreakingFindings(t *testing.T) {
	manual := breakingEntry("0.0.385", "hand edit agents.toml.", "then run jq.")
	heals := healingEntry("0.0.390", "init-heals: init rewrites the hooks.")

	got := breakingFindings("0.0.380", "0.0.396", []verb.ChangelogEntry{heals, manual})
	if len(got) != 2 {
		t.Fatalf("want one finding per unacknowledged release, got %+v", got)
	}
	for _, f := range got {
		if f.ID != health.IDBreakingUnacknowledged {
			t.Errorf("finding ID = %q, want %q", f.ID, health.IDBreakingUnacknowledged)
		}
		if !strings.Contains(f.Detail, "0.0.380") || !strings.Contains(f.Detail, "0.0.396") {
			t.Errorf("detail must name the stamp and the binary: %q", f.Detail)
		}
		if f.Remediation == "" || !strings.Contains(f.Remediation, "satelle init") {
			t.Errorf("remediation must name satelle init: %q", f.Remediation)
		}
	}
	if got[0].Artifact != "0.0.390" || got[0].Severity != health.SeverityError {
		t.Errorf("init-heals release must be an Error naming 0.0.390: %+v", got[0])
	}
	if got[1].Artifact != "0.0.385" || got[1].Severity != health.SeverityWarn ||
		!strings.Contains(got[1].Detail, "hand edit agents.toml.; then run jq.") {
		t.Errorf("manual release must be a Warn carrying its bullets: %+v", got[1])
	}

	// Range membership ((deployed, binVer]) is verb.ChangelogRange's job, so a
	// stamp AT a release never hands it to the classifier; only the ordering of
	// stamp and binary is decided here.
	if fs := breakingFindings("0.0.396", "0.0.396", []verb.ChangelogEntry{heals, manual}); len(fs) != 0 {
		t.Errorf("a current stamp must report nothing, got %+v", fs)
	}
	if fs := breakingFindings("0.0.380", "0.0.396", []verb.ChangelogEntry{{Version: "0.0.390"}}); len(fs) != 0 {
		t.Errorf("a non-breaking gap must report nothing, got %+v", fs)
	}
}

// TestBreakingDriftFindingsQuietCases: dev builds, uninitialised and unstamped
// repos yield no finding (the gate itself refuses the unstamped one).
func TestBreakingDriftFindingsQuietCases(t *testing.T) {
	if fs := breakingDriftFindings(t.TempDir()); len(fs) != 0 {
		t.Errorf("uninitialised repo: %+v", fs)
	}
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if fs := breakingDriftFindings(repo); len(fs) != 0 {
		t.Errorf("unstamped repo: %+v", fs)
	}
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName), []byte("satelle.version: 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isDevVersion(strings.TrimSpace(buildinfo.Resolve().Version)) {
		if fs := breakingDriftFindings(repo); len(fs) != 0 {
			t.Errorf("dev build must report nothing: %+v", fs)
		}
	}
}

// TestInitRestampsBehindABreakingRelease (sty_6e143870 AC3): `satelle init` is the
// re-stamp in both cases — a repo behind a manual-migration release (warned) and
// one with no stamp at all (refused) — and afterwards the gate is quiet. init is
// not store-backed, so it can never sit behind the refusal it heals.
func TestInitRestampsBehindABreakingRelease(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "0.0.900"
	t.Cleanup(func() { buildinfo.Version = old })

	initCmd, _, err := NewRootCmd().Find([]string{"init"})
	if err != nil || initCmd == nil || initCmd.Name() != "init" {
		t.Fatalf("init command not found: %v", err)
	}
	if initCmd.Annotations[storeAnnotation] == "1" {
		t.Fatal("init is store-backed, so a Breaking refusal would block its own heal")
	}

	for name, stamp := range map[string]string{"behind a manual release": "satelle.version: 0.0.100\n", "no stamp": ""} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if err := runInitTest(t, io.Discard, repo); err != nil {
				t.Fatalf("runInit: %v", err)
			}
			stampPath := filepath.Join(repo, config.DefaultDataDir, deployedVersionName)
			if stamp == "" {
				_ = os.Remove(stampPath)
			} else if err := os.WriteFile(stampPath, []byte(stamp), 0o644); err != nil {
				t.Fatal(err)
			}
			before, berr := checkBreakingDrift(repo)
			if before == nil && berr == nil {
				t.Fatal("fixture must be behind the gate before init")
			}
			if err := runInit(io.Discard, repo, false, nil); err != nil {
				t.Fatalf("re-init: %v", err)
			}
			if got := readDeployedVersion(filepath.Join(repo, config.DefaultDataDir)); got != "0.0.900" {
				t.Fatalf("init must re-stamp to the binary version, stamp = %q", got)
			}
			if bw, err := checkBreakingDrift(repo); bw != nil || err != nil {
				t.Fatalf("after init the gate must be quiet: warn=%v err=%v", bw, err)
			}
		})
	}
}
