package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
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

func testCred(url string) hosted.Credential {
	return hosted.Credential{
		ServerURL:    url,
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		Scope:        "mcp",
		DisplayName:  "Ada",
		Email:        "ada@example.com",
		PrincipalID:  "usr_1",
		ExpiresAt:    "2026-10-06T01:00:00Z",
		CreatedAt:    "2026-10-06T00:00:00Z",
	}
}

// credFile writes a credentials file holding creds at path (creating its
// directory) through the real store, so the fixture is the production shape.
func credFile(t *testing.T, path string, creds ...hosted.Credential) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store := hosted.FileStore{Path: path}
	for _, c := range creds {
		if err := store.Save(c); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCredguard proves the Makefile guard (sty_18403814, sty_5ba68e2c): it
// fails a run only for a credentials change a test could make — a credential
// added or removed, an identity changed, the file created, deleted or left
// unparseable — and passes a token rewrite that keeps every identity, the way an
// outside satelle process refreshes a hosted session. The wrapped command's own
// failure status is kept.
func TestCredguard(t *testing.T) {
	const keep = "https://keep.example.com"
	const other = "https://other.example.com"
	cred := func(xdg string) string { return filepath.Join(xdg, "satelle", "credentials.toml") }
	seed := func(t *testing.T, creds ...hosted.Credential) string {
		t.Helper()
		xdg := t.TempDir()
		if len(creds) == 0 {
			creds = []hosted.Credential{testCred(keep)}
		}
		credFile(t, cred(xdg), creds...)
		return xdg
	}
	// replaceWith returns a shell command that overwrites the host file with a
	// fixture holding creds — the test's stand-in for a write by the wrapped run.
	replaceWith := func(t *testing.T, creds ...hosted.Credential) string {
		t.Helper()
		fixture := filepath.Join(t.TempDir(), "fixture.toml")
		credFile(t, fixture, creds...)
		return `mkdir -p "$XDG_CONFIG_HOME/satelle" && cp ` + fixture + ` "$XDG_CONFIG_HOME/satelle/credentials.toml"`
	}
	const changed = "host credentials file changed by tests"
	wantFail := func(t *testing.T, xdg, cmd, reason string) {
		t.Helper()
		code, out := runGuard(t, xdg, cmd)
		if code != 1 || !strings.Contains(out, changed) || !strings.Contains(out, reason) {
			t.Fatalf("exit %d, want 1 with %q and %q; output %q", code, changed, reason, out)
		}
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
	t.Run("token refresh passes", func(t *testing.T) {
		xdg := seed(t, testCred(keep), testCred(other))
		rotated := testCred(keep)
		rotated.AccessToken, rotated.RefreshToken = "access-2", "refresh-2"
		rotated.ExpiresAt, rotated.CreatedAt = "2026-10-06T02:00:00Z", "2026-10-06T01:00:00Z"
		cmd := replaceWith(t, rotated, testCred(other))
		before, _ := os.ReadFile(cred(xdg))
		if code, out := runGuard(t, xdg, cmd); code != 0 {
			t.Fatalf("token refresh must pass, exit %d: %s", code, out)
		}
		after, _ := os.ReadFile(cred(xdg))
		if string(before) == string(after) {
			t.Fatal("test setup: the refresh must change the file bytes")
		}
	})
	t.Run("server added fails", func(t *testing.T) {
		xdg := seed(t)
		wantFail(t, xdg, replaceWith(t, testCred(keep), testCred(other)), "server added "+other)
	})
	t.Run("server removed fails", func(t *testing.T) {
		xdg := seed(t, testCred(keep), testCred(other))
		wantFail(t, xdg, replaceWith(t, testCred(keep)), "server removed "+other)
	})
	t.Run("identity changed fails", func(t *testing.T) {
		xdg := seed(t)
		c := testCred(keep)
		c.Email = "grace@example.com"
		wantFail(t, xdg, replaceWith(t, c), "identity changed for "+keep)
	})
	t.Run("file deleted fails", func(t *testing.T) {
		xdg := seed(t)
		wantFail(t, xdg, `rm "$XDG_CONFIG_HOME/satelle/credentials.toml"`, "file removed")
	})
	t.Run("file left unparseable fails", func(t *testing.T) {
		xdg := seed(t)
		wantFail(t, xdg, `echo x >> "$XDG_CONFIG_HOME/satelle/credentials.toml"`, "unparseable")
	})
	t.Run("created from absent fails", func(t *testing.T) {
		wantFail(t, t.TempDir(), replaceWith(t, testCred(keep)), "file appeared")
	})
	t.Run("suite failure keeps its own exit code", func(t *testing.T) {
		if code, _ := runGuard(t, seed(t), "exit 3"); code != 3 {
			t.Fatalf("exit %d, want 3", code)
		}
	})
	t.Run("suite failure plus change keeps the suite code", func(t *testing.T) {
		xdg := seed(t)
		code, out := runGuard(t, xdg, `rm "$XDG_CONFIG_HOME/satelle/credentials.toml"; exit 3`)
		if code != 3 || !strings.Contains(out, changed) {
			t.Fatalf("exit %d, output %q", code, out)
		}
	})
}
