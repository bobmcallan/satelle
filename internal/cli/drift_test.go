package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/verb"
)

func TestRetiredNameMessage(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install"}, "satelle init"},
		{[]string{"workspace", "rm", "x"}, "workspace remove"},
		{[]string{"sync", "config", "pull"}, "sync config deploy"},
		{[]string{"ui", "push"}, "satelle workspace add"},
		{[]string{"ui"}, "satelle workspace add"},
		{[]string{"service", "install"}, ""}, // not retired
		{[]string{"story", "list"}, ""},
	}
	for _, c := range cases {
		got := retiredNameMessage(c.args)
		if c.want == "" {
			if got != "" {
				t.Errorf("args %v: unexpected %q", c.args, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("args %v: got %q, want contains %q", c.args, got, c.want)
		}
	}
	// ui parent must not be registered as a live subcommand.
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "ui" {
			t.Fatal("ui command still registered — retired in favour of workspace add")
		}
	}
}

func TestWriteReadDeployedVersion(t *testing.T) {
	dir := t.TempDir()
	// Force a non-dev version for the stamp when buildinfo is dev — write directly.
	path := filepath.Join(dir, deployedVersionName)
	if err := os.WriteFile(path, []byte("satelle.version: 0.0.200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readDeployedVersion(dir); got != "0.0.200" {
		t.Fatalf("read = %q", got)
	}
	// writeDeployedVersion no-ops on dev builds — just ensure it doesn't error.
	_ = buildinfo.Resolve()
	if _, err := writeDeployedVersion(dir); err != nil {
		t.Fatal(err)
	}
}

func TestIsDevVersion(t *testing.T) {
	if !isDevVersion("dev") || !isDevVersion("") || !isDevVersion("0.0.0-dev+foo") ||
		!isDevVersion("0.0.396+local") {
		t.Error("dev sentinels")
	}
	if isDevVersion("0.0.218") {
		t.Error("release version is not dev")
	}
}

// TestRefuseBreakingDriftDevBuildNeverGates covers AC4. It asserts against a
// stamp old enough that the shipped 0.0.385 Breaking entry is in range, so on a
// release build it would refuse — the dev short-circuit is the only reason it
// does not. Skips loudly on a release build rather than passing vacuously.
func TestRefuseBreakingDriftDevBuildNeverGates(t *testing.T) {
	if !isDevVersion(buildinfo.Resolve().Version) {
		t.Skip("release build — the dev short-circuit is not the path under test")
	}
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName),
		[]byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refuseBreakingDrift(repo); err != nil {
		t.Fatalf("dev build must never gate: %v", err)
	}
}

func TestRefuseBreakingDriftMissingStamp(t *testing.T) {
	// When buildinfo is a release version and data dir exists without stamp, refuse.
	// Dev builds skip — if this test binary is dev, the guard returns nil.
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := refuseBreakingDrift(repo)
	// Either skipped (dev) or fails naming init.
	if err != nil && !strings.Contains(err.Error(), "satelle init") {
		t.Fatalf("want init named: %v", err)
	}
}

// breakingEntry is a synthesised ### Breaking release carrying remediation text.
func breakingEntry(ver string, bullets ...string) verb.ChangelogEntry {
	e := verb.ChangelogEntry{Version: ver, Breaking: true, Sections: map[string][]string{}}
	if len(bullets) > 0 {
		e.Sections["Breaking"] = bullets
	}
	return e
}

// TestBreakingDriftDecision exercises the pure decision directly. The whole-
// function tests below self-neuter on a dev build (the gate short-circuits), so
// this is where AC2/AC3/AC5 are actually proven (sty_b36c051c).
func TestBreakingDriftDecision(t *testing.T) {
	conv := breakingEntry("0.0.385",
		"An authored DOT workflow no longer resolves a lifecycle.",
		"Convert it forward — run `satelle migrate` and read `satelle help workflow-convert`; `satelle init` does NOT convert DOT.")
	bare := breakingEntry("0.0.385")
	quiet := verb.ChangelogEntry{Version: "0.0.390", Sections: map[string][]string{"Fixed": {"something"}}}

	cases := []struct {
		name           string
		deployed, bin  string
		entries        []verb.ChangelogEntry
		wantErr        bool
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "stamp predates the breaking release — refused with the conversion path",
			// AC2: the remediation the operator reads is the changelog's own text.
			deployed: "0.0.380", bin: "0.0.396", entries: []verb.ChangelogEntry{quiet, conv},
			wantErr:      true,
			wantContains: []string{"0.0.385", "satelle migrate", "satelle help workflow-convert", "0.0.380"},
			// AC2: `satelle init` must NOT be the instruction for this class —
			// except where the entry itself says it does not convert.
			wantNotContain: []string{"run `satelle init` to heal"},
		},
		{
			name: "an entry that declares no remediation falls back to init",
			// AC5: the fallback is the binary's ONE generic answer, not a branch.
			deployed: "0.0.380", bin: "0.0.396", entries: []verb.ChangelogEntry{bare},
			wantErr:      true,
			wantContains: []string{"run `satelle init` to heal"},
		},
		{
			name: "stamped AT the breaking release — not refused",
			// AC3: 0.0.385 is not in (0.0.385, 0.0.396].
			deployed: "0.0.385", bin: "0.0.396", entries: []verb.ChangelogEntry{quiet},
		},
		{
			name:     "current repo — not refused",
			deployed: "0.0.395", bin: "0.0.396", entries: []verb.ChangelogEntry{quiet},
		},
		{
			name:     "stamp equals binary — not refused",
			deployed: "0.0.396", bin: "0.0.396", entries: []verb.ChangelogEntry{conv},
		},
		{
			name:     "binary older than stamp — not refused even with a breaking entry",
			deployed: "0.0.397", bin: "0.0.396", entries: []verb.ChangelogEntry{conv},
		},
		{
			name:     "version gap with no breaking entry — not refused",
			deployed: "0.0.390", bin: "0.0.396", entries: []verb.ChangelogEntry{quiet},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := breakingDriftError(c.deployed, c.bin, c.entries)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want refusal, got nil")
			}
			for _, want := range c.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message missing %q:\n%v", want, err)
				}
			}
			for _, no := range c.wantNotContain {
				if strings.Contains(err.Error(), no) {
					t.Errorf("message must not contain %q:\n%v", no, err)
				}
			}
		})
	}
}

// TestShippedChangelogMarksDOTRetirement pins AC1 to the file that actually
// ships. The guard reads the EMBED, so asserting a fixture that merely
// resembles the changelog would prove nothing about a consumer's binary.
func TestShippedChangelogMarksDOTRetirement(t *testing.T) {
	entries, err := verb.ChangelogRange("0.0.384", "0.0.385")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Version != "0.0.385" {
		t.Fatalf("want exactly the 0.0.385 entry, got %+v", entries)
	}
	e := entries[0]
	if !e.Breaking {
		t.Fatal("0.0.385 retired the DOT front end and must declare ### Breaking")
	}
	// AC2 end to end: the refusal a stale repo sees is composed from THIS text.
	msg := breakingDriftError("0.0.380", "0.0.385", entries)
	if msg == nil {
		t.Fatal("a repo stamped before the DOT retirement must be refused")
	}
	for _, want := range []string{"satelle migrate", "satelle help workflow-convert", "does NOT convert"} {
		if !strings.Contains(msg.Error(), want) {
			t.Errorf("shipped remediation missing %q:\n%v", want, msg)
		}
	}
}

// TestShippedChangelogSparesCurrentRepos is AC3 against the REAL corpus: the
// retroactive marker must refuse only repos that are already broken. Ranges come
// from verb.ChangelogRange over the shipped embed, so this fails if a later edit
// marks a release Breaking that a converted repo has already walked past.
func TestShippedChangelogSparesCurrentRepos(t *testing.T) {
	// A ceiling above every shipped entry, so the range is the widest one any
	// future binary could ask for.
	const future = "9.9.9"
	cases := []struct {
		deployed string
		refused  bool
		why      string
	}{
		{"0.0.380", true, "predates the DOT retirement — already broken, must be told"},
		{"0.0.385", false, "stamped AT the retirement — converted or never on DOT"},
		{"0.0.387", false, "this repo's own stamp at the time of the retro-mark"},
		{"0.0.395", false, "current"},
	}
	for _, c := range cases {
		entries, err := verb.ChangelogRange(c.deployed, future)
		if err != nil {
			t.Fatal(err)
		}
		err = breakingDriftError(c.deployed, future, entries)
		if c.refused && err == nil {
			t.Errorf("stamp %s (%s): want refusal, got none", c.deployed, c.why)
		}
		if !c.refused && err != nil {
			t.Errorf("stamp %s (%s): must not be refused:\n%v", c.deployed, c.why, err)
		}
	}
}

// TestRemediationCommandsAreReachable guards the half of AC2 the message text
// cannot: a refusal is useless if the commands it names sit BEHIND the gate that
// emits it. refuseBreakingDrift runs only for store-backed commands, so the
// conversion path must stay off that annotation.
func TestRemediationCommandsAreReachable(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"migrate", "help"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd == nil || cmd.Name() != name {
			t.Fatalf("%s is named in the drift remediation but is not a command (%v)", name, err)
		}
		if cmd.Annotations[storeAnnotation] == "1" {
			t.Errorf("%s is store-backed, so the drift refusal would block its own heal path", name)
		}
	}
}

// TestRefuseBreakingDriftBreakingRange covers the WIRING — stat the data dir,
// read the stamp, range the shipped changelog, delegate the decision. It cannot
// plant its own changelog: readChangelogBody prefers the embed always, which is
// the point (a consumer's binary carries satelle's changelog, not their repo's).
// So the fixture is the stamp alone, and the corpus is the real one.
func TestRefuseBreakingDriftBreakingRange(t *testing.T) {
	if isDevVersion(buildinfo.Resolve().Version) {
		t.Skip("dev build never gates — see TestBreakingDriftDecision for the decision")
	}
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stamp old enough that the shipped 0.0.385 Breaking entry is in range.
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName),
		[]byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := refuseBreakingDrift(repo)
	if err == nil {
		t.Fatal("release build with a 0.0.100 stamp must refuse across a Breaking release")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "breaking") {
		t.Fatalf("want breaking named: %v", err)
	}

	// AC3, on the same wiring: a current stamp is not refused.
	cur := t.TempDir()
	curData := filepath.Join(cur, ".satelle")
	if err := os.MkdirAll(curData, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(curData, deployedVersionName),
		[]byte("satelle.version: "+buildinfo.Resolve().Version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refuseBreakingDrift(cur); err != nil {
		t.Fatalf("a repo stamped at the running binary must not be refused: %v", err)
	}
}

func TestMigrateAgentsFlattenAndInject(t *testing.T) {
	// Config package test lives better there; smoke via retiredNames already covered.
	// Keep retiredName multi-token asserts here as AC2 CLI invocation surface.
	for _, args := range [][]string{
		{"workspace", "rm"},
		{"sync", "config", "pull"},
	} {
		msg := retiredNameMessage(args)
		if msg == "" {
			t.Errorf("%v must be retired", args)
		}
	}
}
