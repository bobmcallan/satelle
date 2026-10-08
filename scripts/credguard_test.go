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
	return runGuardEnv(t, []string{"XDG_CONFIG_HOME=" + xdg}, shellCmd)
}

// runGuardEnv runs credguard.sh over a shell command with env appended to the
// test process's environment (later entries win).
func runGuardEnv(t *testing.T, env []string, shellCmd string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "credguard.sh", "--", "sh", "-c", shellCmd)
	cmd.Env = append(os.Environ(), env...)
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
	t.Run("host", credguardHost)
	t.Run("fails closed", credguardFailsClosed)
}

// fakeHost builds a temporary HOME holding the layout the host guard watches
// (sty_1b739a74) and returns the env that points the guard at it. The real
// SATELLE_TEST_HOST_HOME is cleared so the fake HOME is the one resolved, and
// the Go caches are pinned to the real ones so the helper still builds offline.
func fakeHost(t *testing.T) (home string, env []string) {
	t.Helper()
	home = t.TempDir()
	for rel, body := range map[string]string{
		".satelle/config.toml":              "a = 1\n",
		".satelle/document-sync-state.json": `{"n":1}`,
		".satelle/repo-0123abcd/db-wal":     "w1",
		".satelle/serve/mirror.db-wal":      "w1",
		".local/bin/satelle":                "v1",
	} {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	goenv := func(name string) string {
		out, err := exec.Command("go", "env", name).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", name, err)
		}
		return strings.TrimSpace(string(out))
	}
	return home, []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"SATELLE_TEST_HOST_HOME=",
		"GOCACHE=" + goenv("GOCACHE"),
		"GOMODCACHE=" + goenv("GOMODCACHE"),
	}
}

// credguardHost proves the host-surface block (sty_1b739a74), run as the "host"
// group of TestCredguard: a new or content-changed top-level entry under the
// real ~/.satelle, or a created, removed or content-changed installed binary,
// fails the run and is named; a no-op touch and the churn a live satelled does
// on its own do not.
func credguardHost(t *testing.T) {
	const changed = "host ~/.satelle or installed binaries changed by tests"
	cases := []struct {
		name string
		cmd  string
		code int
		want string // substring of the output when code != 0
	}{
		{"no-op passes", "true", 0, ""},
		{"new top-level dir fails", `mkdir "$HOME/.satelle/scratch-x"`, 1, "added entry scratch-x"},
		{"new top-level file fails", `echo x > "$HOME/.satelle/new.toml"`, 1, "added entry new.toml"},
		{"edited top-level file fails", `echo '# x' >> "$HOME/.satelle/config.toml"`, 1, "changed file config.toml"},
		{"removed top-level file fails", `rm "$HOME/.satelle/config.toml"`, 1, "removed entry config.toml"},
		{"new satelled fails", `echo d > "$HOME/.local/bin/satelled"`, 1, "appeared bin satelled"},
		{"changed satelle fails", `echo v2 > "$HOME/.local/bin/satelle"`, 1, "changed bin satelle"},
		{"removed satelle fails", `rm "$HOME/.local/bin/satelle"`, 1, "removed bin satelle"},
		{"touched satelle passes", `touch "$HOME/.local/bin/satelle"`, 0, ""},
		{"key dir contents pass", `echo w2 > "$HOME/.satelle/repo-0123abcd/db-wal"; echo j > "$HOME/.satelle/repo-0123abcd/db-journal"`, 0, ""},
		{"serve contents pass", `echo w2 > "$HOME/.satelle/serve/mirror.db-wal"`, 0, ""},
		{"document sync cursor passes", `echo '{"n":2}' > "$HOME/.satelle/document-sync-state.json"`, 0, ""},
		{"suite failure plus change keeps the suite code", `mkdir "$HOME/.satelle/scratch-x"; exit 3`, 3, "added entry scratch-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, env := fakeHost(t)
			code, out := runGuardEnv(t, env, tc.cmd)
			if code != tc.code {
				t.Fatalf("exit %d, want %d; output %q", code, tc.code, out)
			}
			if tc.want != "" && (!strings.Contains(out, changed) || !strings.Contains(out, tc.want)) {
				t.Fatalf("output %q, want %q and %q", out, changed, tc.want)
			}
			if tc.want == "" && strings.Contains(out, "credguard:") {
				t.Fatalf("unexpected guard output %q", out)
			}
		})
	}
	t.Run("SATELLE_HOME and XDG are not the host", func(t *testing.T) {
		_, env := fakeHost(t)
		env = append(env, "SATELLE_HOME="+t.TempDir())
		if code, out := runGuardEnv(t, env, `mkdir "$SATELLE_HOME/scratch-x"`); code != 0 {
			t.Fatalf("a write into the isolated SATELLE_HOME must not trip the host guard, exit %d: %s", code, out)
		}
		if code, out := runGuardEnv(t, env, `mkdir "$HOME/.satelle/scratch-x"`); code != 1 || !strings.Contains(out, "added entry scratch-x") {
			t.Fatalf("a write into the real host must trip even with SATELLE_HOME isolated, exit %d: %s", code, out)
		}
	})
	t.Run("SATELLE_TEST_HOST_HOME selects the host", func(t *testing.T) {
		_, env := fakeHost(t)
		host, _ := fakeHost(t)
		env = append(env, "SATELLE_TEST_HOST_HOME="+host)
		if code, out := runGuardEnv(t, env, `mkdir "$SATELLE_TEST_HOST_HOME/.satelle/scratch-x"`); code != 1 || !strings.Contains(out, "added entry scratch-x") {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
}

// credguardFailsClosed proves, as the "fails closed" group of TestCredguard,
// that the guard exits 2 with a "failing closed" message when it cannot build
// its helper or cannot take the host snapshot, rather than letting the wrapped
// command pass unguarded.
func credguardFailsClosed(t *testing.T) {
	failsClosed := func(t *testing.T, env []string, msg string) {
		t.Helper()
		code, out := runGuardEnv(t, env, "true")
		if code != 2 || !strings.Contains(out, "failing closed") || !strings.Contains(out, msg) {
			t.Fatalf("exit %d, want 2 with %q and %q; output %q", code, "failing closed", msg, out)
		}
	}
	t.Run("helper cannot be built: no Go toolchain", func(t *testing.T) {
		_, env := fakeHost(t)
		bin := t.TempDir()
		for _, tool := range []string{"sh", "mktemp", "rm", "dirname", "sed"} {
			p, err := exec.LookPath(tool)
			if err != nil {
				t.Skipf("%s not on PATH: %v", tool, err)
			}
			if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
		failsClosed(t, append(env, "PATH="+bin), "cannot build scripts/credfp")
	})
	t.Run("helper cannot be built: broken build", func(t *testing.T) {
		_, env := fakeHost(t)
		failsClosed(t, append(env, "GOFLAGS=-mod=nonexistent"), "cannot build scripts/credfp")
	})
	t.Run("host snapshot cannot be taken", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads through mode 000")
		}
		home, env := fakeHost(t)
		satelleHome := filepath.Join(home, ".satelle")
		if err := os.Chmod(satelleHome, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(satelleHome, 0o755) })
		failsClosed(t, env, "cannot snapshot host surface")
	})
}
