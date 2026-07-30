package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestParseLatestTag(t *testing.T) {
	tag, err := parseLatestTag([]byte(`{"tag_name":"v0.0.9","name":"v0.0.9"}`))
	if err != nil || tag != "v0.0.9" {
		t.Fatalf("parseLatestTag = %q, %v", tag, err)
	}
	if _, err := parseLatestTag([]byte(`{"name":"x"}`)); err == nil {
		t.Error("expected error when tag_name is absent")
	}
}

// TestParseMainPID: the MainPID parser accepts both `--value` (bare "1234") and
// raw `MainPID=1234` output, and rejects inactive (0) / never-ours (1) / garbage
// so the sudo-free signal restart never SIGTERMs the wrong process (sty_1ac9f095).
func TestParseMainPID(t *testing.T) {
	cases := map[string]int{
		"894365":         894365, // --value form
		"MainPID=894365": 894365, // property form
		" 894365\n":      894365, // whitespace tolerated
		"0":              0,      // inactive → no pid
		"MainPID=0":      0,
		"1":              0, // PID 1 is never our serve
		"":               0,
		"nope":           0,
	}
	for in, want := range cases {
		if got := parseMainPID(in); got != want {
			t.Errorf("parseMainPID(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestAssetName(t *testing.T) {
	got := assetName("v0.0.9")
	want := fmt.Sprintf("satelle-v0.0.9-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Errorf("assetName = %q, want %q", got, want)
	}
}

func TestUpdateAvailable(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.0.6", "v0.0.9", true},
		{"0.0.9", "v0.0.9", false}, // leading v normalised
		{"v0.0.9", "v0.0.9", false},
		{"0.0.0-dev+abc-dirty", "v0.0.9", true},
	}
	for _, c := range cases {
		if got := updateAvailable(c.current, c.latest); got != c.want {
			t.Errorf("updateAvailable(%q,%q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// TestFromSourceProceeds locks the behaviour `satelle update` shares with
// `claude update` (sty_d2936170): a from-source/dev build is NOT blocked — it
// proceeds to install whenever the latest release differs, and is a graceful
// no-op ("already up to date") only when it already matches. The old
// from-source refusal guard was removed; the dev escape hatch is `--local`
// (sty_fe3ee313), not a refusal.
func TestFromSourceProceeds(t *testing.T) {
	// A from-source dev build with a differing latest release proceeds to install.
	if !updateAvailable("0.0.0-dev+abc-dirty", "v0.0.9") {
		t.Error("a from-source build must self-update when the latest release differs (no refusal)")
	}
	// Equal versions are a graceful no-op, not an error.
	if updateAvailable("v0.0.9", "v0.0.9") {
		t.Error("an up-to-date install must report no update available")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("a fake binary")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:]) + "  satelle-v0.0.9-linux-amd64"
	if err := verifyChecksum(data, good); err != nil {
		t.Errorf("matching checksum rejected: %v", err)
	}
	if err := verifyChecksum(data, "deadbeef  satelle"); err == nil {
		t.Error("mismatched checksum accepted")
	}
}

func TestReplaceExecutable(t *testing.T) {
	target := filepath.Join(t.TempDir(), "bin", "satelle") // dir does not exist yet
	if err := replaceExecutable(target, []byte("v2 binary")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "v2 binary" {
		t.Fatalf("target content = %q, %v", got, err)
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("target not executable: %v", info.Mode())
	}
	// No leftover temp files in the dir.
	entries, _ := os.ReadDir(filepath.Dir(target))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".satelle-update-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestDownloadAndReplaceFrom drives the full download→verify→replace path
// against a local fixture server — no network, no real binary.
func TestDownloadAndReplaceFrom(t *testing.T) {
	bin := []byte("the new satelle binary bytes")
	sum := sha256.Sum256(bin)
	name := "satelle-v9.9.9-linux-amd64"

	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux.HandleFunc("/"+name+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "satelle")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := downloadAndReplaceFrom(context.Background(), srv.URL, name, target); err != nil {
		t.Fatalf("downloadAndReplaceFrom: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(bin) {
		t.Errorf("target not replaced with new bytes: %q", got)
	}

	// A corrupted checksum aborts and leaves the existing binary intact.
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(bin) })
	mux2.HandleFunc("/"+name+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "deadbeef  %s\n", name)
	})
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	keep := filepath.Join(t.TempDir(), "satelle")
	_ = os.WriteFile(keep, []byte("keep me"), 0o755)
	if err := downloadAndReplaceFrom(context.Background(), srv2.URL, name, keep); err == nil {
		t.Error("expected sha mismatch error")
	}
	if got, _ := os.ReadFile(keep); string(got) != "keep me" {
		t.Errorf("binary replaced despite checksum failure: %q", got)
	}
}

func TestFirstPrefixedTag(t *testing.T) {
	body := []byte(`[
	  {"tag_name":"v0.0.285"},
	  {"tag_name":"serve-v0.0.2"},
	  {"tag_name":"serve-v0.0.1"}
	]`)
	got, err := firstPrefixedTag(body, "serve-v")
	if err != nil || got != "serve-v0.0.2" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := firstPrefixedTag(body, "nope-"); err == nil {
		t.Fatal("expected error for missing prefix")
	}
}

func TestAssetNameForServeTag(t *testing.T) {
	// ensure serve- tag prefix is stripped (asset uses vY, not serve-vY twice).
	name := assetNameFor("satelle-serve", "serve-v0.0.2")
	if !strings.HasPrefix(name, "satelle-serve-v0.0.2-") {
		t.Fatalf("name=%q want satelle-serve-v0.0.2-…", name)
	}
	if strings.Contains(name, "satelle-serve-serve-") {
		t.Fatalf("double serve- prefix: %q", name)
	}
}

// --- Pure parsers/classifiers (sty_c344d080) ---

func TestParseUnitStartLimited(t *testing.T) {
	cases := map[string]bool{
		"start-limit-hit":    true,
		"START-LIMIT-HIT":    true,
		" start-limit-hit\n": true,
		"exit-code":          false,
		"success":            false,
		"":                   false,
	}
	for in, want := range cases {
		if got := parseUnitStartLimited(in); got != want {
			t.Errorf("parseUnitStartLimited(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseExecStartBinary(t *testing.T) {
	unit := "[Unit]\nDescription=x\n\n[Service]\nExecStart=/home/u/.local/bin/satelle-serve --addr 0.0.0.0 --port 8787\nRestart=always\n"
	if got := parseExecStartBinary(unit); got != "/home/u/.local/bin/satelle-serve" {
		t.Errorf("parseExecStartBinary = %q", got)
	}
	if got := parseExecStartBinary("[Service]\nExecStart=/bin/only\n"); got != "/bin/only" {
		t.Errorf("no-args ExecStart: got %q", got)
	}
	if got := parseExecStartBinary("[Service]\nUser=x\n"); got != "" {
		t.Errorf("no ExecStart line should return empty, got %q", got)
	}
}

func TestParseListenInode(t *testing.T) {
	// Real /proc/net/tcp shape: header line, then rows; local_address is
	// hex_ip:hex_port, state 0A = LISTEN. Port 8787 = 0x2253.
	body := "  sl  local_address rem_address   st tx_queue:rx_queue tr:tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:2253 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 55555 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 66666 1 0000000000000000 100 0 0 10 0\n"
	if got := parseListenInode(body, 8787); got != "55555" {
		t.Errorf("parseListenInode(8787) = %q, want 55555", got)
	}
	if got := parseListenInode(body, 9999); got != "" {
		t.Errorf("parseListenInode(9999) = %q, want empty", got)
	}
}

// --- Exe identity (AC1) ---

func TestIdentityFromPathAndPID(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "wanted")
	if err := os.WriteFile(want, []byte("binary bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("different bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantID, ok := identityFromPath(want)
	if !ok {
		t.Fatal("identityFromPath(want) not ok")
	}
	otherID, ok := identityFromPath(other)
	if !ok {
		t.Fatal("identityFromPath(other) not ok")
	}
	if identitiesMatch(wantID, otherID) {
		t.Error("distinct files must not match")
	}
	sameID, ok := identityFromPath(want)
	if !ok || !identitiesMatch(wantID, sameID) {
		t.Error("same file must match itself")
	}
	if _, ok := identityFromPath(filepath.Join(dir, "missing")); ok {
		t.Error("missing path must return ok=false")
	}

	// identityFromPID stats procRoot/<pid>/exe directly (a symlink to the real
	// file), mirroring /proc/<pid>/exe — must resolve to the SAME identity as
	// identityFromPath on the target.
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "4242")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(want, filepath.Join(pidDir, "exe")); err != nil {
		t.Fatal(err)
	}
	pidID, ok := identityFromPID(procRoot, 4242)
	if !ok || !identitiesMatch(pidID, wantID) {
		t.Errorf("identityFromPID via symlink should match the target file's identity")
	}
	if _, ok := identityFromPID(procRoot, 9999); ok {
		t.Error("identityFromPID for a nonexistent pid must return ok=false")
	}
	if _, ok := identityFromPID(procRoot, 0); ok {
		t.Error("identityFromPID(pid<=0) must return ok=false")
	}
}

// writeFakeCgroupPID creates procRoot/<pid>/cgroup naming unitCgroupLine, and
// (when exeTarget != "") a procRoot/<pid>/exe symlink to it — a minimal fake
// /proc process entry (sty_c344d080 AC2/AC6).
func writeFakeCgroupPID(t *testing.T, procRoot string, pid int, unitCgroupLine, exeTarget string) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::"+unitCgroupLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exeTarget != "" {
		if err := os.Symlink(exeTarget, filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindPIDByCgroup(t *testing.T) {
	root := t.TempDir()
	writeFakeCgroupPID(t, root, 100, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", "")
	// Substring-collision guard: a DIFFERENT unit name that merely contains
	// "satelle.service" as a substring must not false-match.
	writeFakeCgroupPID(t, root, 200, "/system.slice/notsatelle.service", "")
	if got := findPIDByCgroup(root, "satelle.service"); got != 100 {
		t.Errorf("findPIDByCgroup = %d, want 100", got)
	}
	if got := findPIDByCgroup(t.TempDir(), "satelle.service"); got != 0 {
		t.Errorf("empty proc tree: got %d, want 0", got)
	}
}

func TestFindPIDByListenPort(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "  sl  local_address rem_address   st tx_queue:rx_queue tr:tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:2253 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 77777 1 0 100 0 0 10 0\n"
	if err := os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pidDir := filepath.Join(root, "321", "fd")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[77777]", filepath.Join(pidDir, "3")); err != nil {
		t.Fatal(err)
	}
	if got := findPIDByListenPort(root, 8787); got != 321 {
		t.Errorf("findPIDByListenPort = %d, want 321", got)
	}
	if got := findPIDByListenPort(root, 9999); got != 0 {
		t.Errorf("no matching port: got %d, want 0", got)
	}
}

// setupWantedIdentity points wantedExeIdentity's fallback (installTarget) at a
// controllable file, with no real unit files in play: HOME is an empty temp dir
// (no ~/.config/systemd/user/satelle.service) and the real /etc/systemd/system
// path never exists in a test sandbox. Returns the resolved "wanted" binary path.
func setupWantedIdentity(t *testing.T) string {
	t.Helper()
	testutil.IsolateHome(t) // servicePort()'s config.LoadGlobal() refuses to run without SATELLE_HOME set
	home := t.TempDir()
	t.Setenv("HOME", home)
	installDir := t.TempDir()
	t.Setenv("SATELLE_INSTALL_DIR", installDir)
	target := filepath.Join(installDir, "satelle")
	if err := os.WriteFile(target, []byte("the installed binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestWantedExeIdentity(t *testing.T) {
	target := setupWantedIdentity(t)
	id, ok := wantedExeIdentity("/proc")
	if !ok {
		t.Fatal("wantedExeIdentity not ok")
	}
	direct, _ := identityFromPath(target)
	if !identitiesMatch(id, direct) {
		t.Error("wantedExeIdentity should fall back to installTarget() when no unit file exists")
	}
}

func TestReportRestartOutcome(t *testing.T) {
	cases := []struct {
		name    string
		pid     int
		matched bool
		want    string
	}{
		{"no pid", 0, false, "could not locate its process"},
		{"matched", 42, true, "onto the new binary"},
		{"not matched", 42, false, "could not confirm it is running the new binary"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		reportRestartOutcome(&buf, "user unit", c.pid, c.matched)
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("%s: output = %q, want substring %q", c.name, buf.String(), c.want)
		}
	}
}

// --- restartServiceIfRunningRoot end-to-end scenarios (AC6) ---
//
// Every case overrides restartHooks so NOTHING here ever shells out to the real
// systemctl or touches the operator's actual satelle.service unit — the plan's
// explicit "avoid real systemd in unit tests" (sty_c344d080).

func withRestartHooks(t *testing.T, h struct {
	lookSystemctl        func() error
	userUnitActive       func() bool
	userUnitMainPID      func() int
	restartUserUnit      func() error
	systemUnitMainPID    func() int
	systemUnitRestartAlw func() bool
	signalTerm           func(int) error
	systemUnitRespawned  func(int) (int, bool)
	systemUnitStartLtd   func() bool
}) {
	t.Helper()
	prev := restartHooks
	restartHooks = h
	t.Cleanup(func() { restartHooks = prev })
	prevAttempts, prevInterval := identityPollAttempts, identityPollInterval
	identityPollAttempts, identityPollInterval = 2, time.Millisecond
	t.Cleanup(func() { identityPollAttempts, identityPollInterval = prevAttempts, prevInterval })
}

// noSystemctlPath is the shared "neither systemctl path is reachable" hook set —
// every scenario that must fall through to bus-independent discovery uses it.
func noSystemctlPath() struct {
	lookSystemctl        func() error
	userUnitActive       func() bool
	userUnitMainPID      func() int
	restartUserUnit      func() error
	systemUnitMainPID    func() int
	systemUnitRestartAlw func() bool
	signalTerm           func(int) error
	systemUnitRespawned  func(int) (int, bool)
	systemUnitStartLtd   func() bool
} {
	return struct {
		lookSystemctl        func() error
		userUnitActive       func() bool
		userUnitMainPID      func() int
		restartUserUnit      func() error
		systemUnitMainPID    func() int
		systemUnitRestartAlw func() bool
		signalTerm           func(int) error
		systemUnitRespawned  func(int) (int, bool)
		systemUnitStartLtd   func() bool
	}{
		lookSystemctl:        func() error { return nil },
		userUnitActive:       func() bool { return false },
		userUnitMainPID:      func() int { return 0 },
		restartUserUnit:      func() error { return fmt.Errorf("bus unreachable") },
		systemUnitMainPID:    func() int { return 0 },
		systemUnitRestartAlw: func() bool { return false },
		signalTerm:           func(int) error { return fmt.Errorf("not called") },
		systemUnitRespawned:  func(int) (int, bool) { return 0, false },
		systemUnitStartLtd:   func() bool { return false },
	}
}

// Scenario 1: fresh install, no existing process anywhere discoverable.
func TestRestartServiceIfRunning_FreshInstallNoProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	setupWantedIdentity(t)
	withRestartHooks(t, noSystemctlPath())
	var buf bytes.Buffer
	restartServiceIfRunningRoot(&buf, t.TempDir()) // empty fake /proc: no cgroup hit, no net/tcp file
	if !strings.Contains(buf.String(), "no running satelle.service process was found") {
		t.Errorf("output = %q", buf.String())
	}
}

// Scenario 2: stale process under a REACHABLE user unit — restart is commanded,
// but identity verification is what decides the message (AC1), not the restart
// command's exit code.
func TestRestartServiceIfRunning_StaleUnderReachableUserUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	target := setupWantedIdentity(t)
	procRoot := t.TempDir()
	// Mismatched case: the process never picks up the new binary.
	staleFile := filepath.Join(t.TempDir(), "stale")
	if err := os.WriteFile(staleFile, []byte("old bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCgroupPID(t, procRoot, 900, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", staleFile)
	h := noSystemctlPath()
	h.userUnitActive = func() bool { return true }
	h.restartUserUnit = func() error { return nil } // exit 0 — but identity below still shows the OLD binary
	h.userUnitMainPID = func() int { return 900 }
	withRestartHooks(t, h)
	var buf bytes.Buffer
	restartServiceIfRunningRoot(&buf, procRoot)
	if !strings.Contains(buf.String(), "could not confirm it is running the new binary") {
		t.Errorf("exit-0 restart with a stale identity must not claim success: %q", buf.String())
	}

	// Matched case: the process now IS the wanted binary (symlink points at target).
	procRoot2 := t.TempDir()
	writeFakeCgroupPID(t, procRoot2, 901, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", target)
	h.userUnitMainPID = func() int { return 901 }
	withRestartHooks(t, h)
	var buf2 bytes.Buffer
	restartServiceIfRunningRoot(&buf2, procRoot2)
	if !strings.Contains(buf2.String(), "onto the new binary") {
		t.Errorf("matched identity must report success: %q", buf2.String())
	}
}

// Scenario 3: stale process discoverable only via cgroup, no systemctl path
// reachable — must report "supervisor is unreachable" and must NOT signal the
// process (a user unit is Restart=on-failure; a SIGTERM would stop it).
func TestRestartServiceIfRunning_StaleUnreachableUserUnitCgroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	setupWantedIdentity(t)
	procRoot := t.TempDir()
	staleFile := filepath.Join(t.TempDir(), "stale")
	if err := os.WriteFile(staleFile, []byte("old bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCgroupPID(t, procRoot, 902, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", staleFile)
	signalled := false
	h := noSystemctlPath()
	h.signalTerm = func(int) error { signalled = true; return nil }
	withRestartHooks(t, h)
	var buf bytes.Buffer
	restartServiceIfRunningRoot(&buf, procRoot)
	if !strings.Contains(buf.String(), "supervisor is unreachable") {
		t.Errorf("output = %q", buf.String())
	}
	if signalled {
		t.Error("a cgroup-discovered user-unit process must never be signalled (Restart=on-failure would STOP it)")
	}
}

// Scenario 4: system unit is start-limited (systemd exhausted restart attempts) —
// must be named explicitly, not folded into the generic "did not respawn" line.
func TestRestartServiceIfRunning_StartLimited(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	setupWantedIdentity(t)
	h := noSystemctlPath()
	h.systemUnitMainPID = func() int { return 903 }
	h.systemUnitRestartAlw = func() bool { return true }
	h.signalTerm = func(int) error { return nil }
	h.systemUnitRespawned = func(int) (int, bool) { return 0, false } // did not come back
	h.systemUnitStartLtd = func() bool { return true }
	withRestartHooks(t, h)
	var buf bytes.Buffer
	restartServiceIfRunningRoot(&buf, t.TempDir())
	if !strings.Contains(buf.String(), "start-limited") || !strings.Contains(buf.String(), "reset-failed") {
		t.Errorf("output = %q", buf.String())
	}
}

// Scenario 5: already current — the live process (found via cgroup) already
// matches the newly installed binary; no restart is attempted.
func TestRestartServiceIfRunning_AlreadyCurrent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	target := setupWantedIdentity(t)
	procRoot := t.TempDir()
	writeFakeCgroupPID(t, procRoot, 904, "/user.slice/user-1000.slice/user@1000.service/app.slice/satelle.service", target)
	signalled := false
	restarted := false
	h := noSystemctlPath()
	h.signalTerm = func(int) error { signalled = true; return nil }
	h.restartUserUnit = func() error { restarted = true; return nil }
	withRestartHooks(t, h)
	var buf bytes.Buffer
	restartServiceIfRunningRoot(&buf, procRoot)
	if !strings.Contains(buf.String(), "already running the new binary — no restart needed") {
		t.Errorf("output = %q", buf.String())
	}
	if signalled || restarted {
		t.Error("an already-current process must not be restarted or signalled")
	}
}

// AC7 regression: the two paths that already worked sudo-free keep working the
// same way — a healthy Restart=always system unit still gets a SIGTERM (not a
// sudo command), and a reachable user unit still gets `systemctl --user restart`
// (not sudo). This guards against the identity-verification addition silently
// requiring sudo anywhere it did not before.
func TestRestartServiceIfRunning_SudoFreePathsUnchanged(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only mechanism")
	}
	target := setupWantedIdentity(t)
	procRoot := t.TempDir()
	writeFakeCgroupPID(t, procRoot, 905, "irrelevant-for-this-case", target)
	restartUserCalled := false
	signalCalled := false
	h := noSystemctlPath()
	h.userUnitActive = func() bool { return true }
	h.restartUserUnit = func() error { restartUserCalled = true; return nil }
	h.userUnitMainPID = func() int { return 905 }
	withRestartHooks(t, h)
	var buf bytes.Buffer
	restartServiceIfRunningRoot(&buf, procRoot)
	if !restartUserCalled {
		t.Error("reachable user unit must still be restarted via systemctl --user (no sudo)")
	}
	if signalCalled {
		t.Error("user-unit path must never signal directly")
	}

	// System unit, Restart=always, healthy respawn — still SIGTERM, no sudo.
	procRoot2 := t.TempDir()
	writeFakeCgroupPID(t, procRoot2, 906, "irrelevant", target)
	signalled := false
	h2 := noSystemctlPath()
	h2.systemUnitMainPID = func() int { return 800 }
	h2.systemUnitRestartAlw = func() bool { return true }
	h2.signalTerm = func(int) error { signalled = true; return nil }
	h2.systemUnitRespawned = func(int) (int, bool) { return 906, true }
	withRestartHooks(t, h2)
	var buf2 bytes.Buffer
	restartServiceIfRunningRoot(&buf2, procRoot2)
	if !signalled {
		t.Error("healthy Restart=always system unit must still be signalled directly (no sudo)")
	}
	if strings.Contains(buf2.String(), "sudo") {
		t.Errorf("a successful respawn path must not mention sudo: %q", buf2.String())
	}
}
