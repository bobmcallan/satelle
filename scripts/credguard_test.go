package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGuard runs credguard.sh over a shell command with XDG_CONFIG_HOME pointing
// at xdg and returns its exit code and combined output.
func runGuard(t *testing.T, xdg, shellCmd string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "credguard.sh", "--", "sh", "-c", shellCmd)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	default:
		t.Fatalf("run credguard: %v", err)
		return -1, ""
	}
}

// TestCredguard proves the Makefile guard (sty_18403814): an untouched host
// credentials file passes, any change (including creating it from absent) fails
// with a message, and the wrapped command's own failure status is kept.
func TestCredguard(t *testing.T) {
	cred := func(xdg string) string { return filepath.Join(xdg, "satelle", "credentials.toml") }
	seed := func(t *testing.T) string {
		t.Helper()
		xdg := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(cred(xdg)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cred(xdg), []byte("[[credential]]\nserver_url = \"https://h\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return xdg
	}

	t.Run("unchanged file passes", func(t *testing.T) {
		if code, out := runGuard(t, seed(t), "true"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("absent and still absent passes", func(t *testing.T) {
		if code, out := runGuard(t, t.TempDir(), "true"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("modified file fails with a message", func(t *testing.T) {
		xdg := seed(t)
		code, out := runGuard(t, xdg, `echo x >> "$XDG_CONFIG_HOME/satelle/credentials.toml"`)
		if code != 1 || !strings.Contains(out, "host credentials file changed by tests") {
			t.Fatalf("exit %d, output %q", code, out)
		}
	})
	t.Run("created from absent fails", func(t *testing.T) {
		xdg := t.TempDir()
		code, out := runGuard(t, xdg, `mkdir -p "$XDG_CONFIG_HOME/satelle" && echo x > "$XDG_CONFIG_HOME/satelle/credentials.toml"`)
		if code != 1 || !strings.Contains(out, "host credentials file changed by tests") {
			t.Fatalf("exit %d, output %q", code, out)
		}
	})
	t.Run("suite failure keeps its own exit code", func(t *testing.T) {
		if code, _ := runGuard(t, seed(t), "exit 3"); code != 3 {
			t.Fatalf("exit %d, want 3", code)
		}
	})
	t.Run("suite failure plus change keeps the suite code", func(t *testing.T) {
		xdg := seed(t)
		code, out := runGuard(t, xdg, `echo x >> "$XDG_CONFIG_HOME/satelle/credentials.toml"; exit 3`)
		if code != 3 || !strings.Contains(out, "host credentials file changed by tests") {
			t.Fatalf("exit %d, output %q", code, out)
		}
	})
}
