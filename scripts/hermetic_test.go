package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHermeticWrapperEnvironment runs hermetic.sh over `env` with a caller
// environment carrying a real-looking HOME, GOPROXY=off, SATELLE_HOME=/x and an
// unlisted variable, and asserts what the wrapped command actually sees.
func TestHermeticWrapperEnvironment(t *testing.T) {
	callerHome := t.TempDir()
	callerEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + callerHome,
		"GOPROXY=off",
		"SATELLE_HOME=/x",
		"SATELLE_TEST_PROBE_X=1",
		"UNLISTED=1",
	}
	// The caller's module cache, as go resolves it in the caller's environment.
	goEnv := exec.Command("go", "env", "GOMODCACHE")
	goEnv.Env = callerEnv
	modOut, err := goEnv.Output()
	if err != nil {
		t.Fatalf("go env GOMODCACHE: %v", err)
	}
	callerModCache := strings.TrimSpace(string(modOut))

	cmd := exec.Command("sh", "hermetic.sh", "--", "env")
	cmd.Env = callerEnv
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hermetic.sh: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}

	if got["HOME"] == "" || got["HOME"] == callerHome {
		t.Errorf("HOME = %q, want a fresh dir different from the caller's %q", got["HOME"], callerHome)
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		if got[k] == "" || strings.HasPrefix(got[k], callerHome) {
			t.Errorf("%s = %q, want a fresh dir outside the caller's HOME", k, got[k])
		}
	}
	for _, p := range filepath.SplitList(got["PATH"]) {
		if strings.Contains(p, "shims") || strings.HasPrefix(p, callerHome) {
			t.Errorf("PATH entry %q is a shim dir or under the caller's HOME", p)
		}
	}
	if got["GOMODCACHE"] != callerModCache {
		t.Errorf("GOMODCACHE = %q, want the caller's %q", got["GOMODCACHE"], callerModCache)
	}
	if got["GOPROXY"] != "off" {
		t.Errorf("GOPROXY = %q, want the forwarded off", got["GOPROXY"])
	}
	if v, ok := got["SATELLE_HOME"]; ok {
		t.Errorf("SATELLE_HOME = %q leaked into the wrapped command", v)
	}
	if got["SATELLE_TEST_HOST_SATELLE_HOME"] != "/x" {
		t.Errorf("SATELLE_TEST_HOST_SATELLE_HOME = %q, want /x", got["SATELLE_TEST_HOST_SATELLE_HOME"])
	}
	if got["SATELLE_TEST_HOST_HOME"] != callerHome {
		t.Errorf("SATELLE_TEST_HOST_HOME = %q, want the caller's %q", got["SATELLE_TEST_HOST_HOME"], callerHome)
	}
	if want := filepath.Join(callerHome, ".config"); got["SATELLE_TEST_HOST_XDG_CONFIG_HOME"] != want {
		t.Errorf("SATELLE_TEST_HOST_XDG_CONFIG_HOME = %q, want %q", got["SATELLE_TEST_HOST_XDG_CONFIG_HOME"], want)
	}
	if got["SATELLE_TEST_PROBE_X"] != "1" {
		t.Errorf("SATELLE_TEST_PROBE_X = %q, want it forwarded", got["SATELLE_TEST_PROBE_X"])
	}
	if v, ok := got["UNLISTED"]; ok {
		t.Errorf("UNLISTED = %q, want it dropped", v)
	}
}

// TestHermeticWrapperCleansUpAndKeepsStatus proves the temp dir is removed and
// the wrapped command's exit status survives.
func TestHermeticWrapperCleansUpAndKeepsStatus(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "home")
	cmd := exec.Command("sh", "hermetic.sh", "--", "sh", "-c", `printf %s "$HOME" > "$1"; exit 7`, "sh", marker)
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 7 {
		t.Fatalf("exit = %v, want status 7 kept", err)
	}
	home, rerr := os.ReadFile(marker)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if _, serr := os.Stat(string(home)); !os.IsNotExist(serr) {
		t.Errorf("sandbox HOME %s still exists after exit (stat err %v)", home, serr)
	}
}
