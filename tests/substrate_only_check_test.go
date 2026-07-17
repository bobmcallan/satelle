//go:build integration

package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubstrateOnlyCheckScript exercises the shipped satelle-substrate-only-check
// ```check block directly (sty_40973fb6): managed footprint accepted, product
// code rejected with offenders named, and [gate] edit_exempt_paths still honored.
func TestSubstrateOnlyCheckScript(t *testing.T) {
	script := extractShippedSubstrateOnlyCheck(t)
	scriptPath := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(scriptPath, []byte(script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("accepts managed footprint", func(t *testing.T) {
		repo := t.TempDir()
		gitInit(t, repo)
		// Baseline commit without the story id so git log --grep only hits the slice.
		mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
		gitCommitAll(t, repo, "baseline")

		sid := "sty_aaa11111"
		mustWrite(t, filepath.Join(repo, ".gitignore"), "# satelle managed\n.satelle/data/\n")
		mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), "{}\n")
		mustWrite(t, filepath.Join(repo, ".grok", "hooks", "satelle.json"), "{}\n")
		mustWrite(t, filepath.Join(repo, ".satelle", "x.md"), "# substrate\n")
		gitCommitAll(t, repo, "init footprint ("+sid+")")

		out, err := runCheckScript(t, scriptPath, repo, sid)
		if err != nil {
			t.Fatalf("managed-footprint slice should exit 0: %v\n%s", err, out)
		}
		if !strings.Contains(out, "substrate-only slice confirmed") {
			t.Errorf("want confirm line, got:\n%s", out)
		}
	})

	t.Run("rejects code slice naming offenders", func(t *testing.T) {
		repo := t.TempDir()
		gitInit(t, repo)
		mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
		gitCommitAll(t, repo, "baseline")

		sid := "sty_bbb22222"
		mustWrite(t, filepath.Join(repo, "cmd", "foo.go"), "package main\n")
		mustWrite(t, filepath.Join(repo, "Makefile"), "all:\n")
		gitCommitAll(t, repo, "code change ("+sid+")")

		out, err := runCheckScript(t, scriptPath, repo, sid)
		if err == nil {
			t.Fatalf("code slice should be rejected, got exit 0:\n%s", out)
		}
		if !strings.Contains(out, "cmd/foo.go") {
			t.Errorf("offenders should name cmd/foo.go:\n%s", out)
		}
		if !strings.Contains(out, "Makefile") {
			t.Errorf("offenders should name Makefile:\n%s", out)
		}
	})

	t.Run("honors edit_exempt_paths extension", func(t *testing.T) {
		repo := t.TempDir()
		gitInit(t, repo)
		mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
		// Seed a satelle.toml so the check can extend the allow-list; commit
		// without the story id so only the later build/ path is in the slice.
		mustWrite(t, filepath.Join(repo, ".satelle", "satelle.toml"),
			"[gate]\nedit_exempt_paths = [\"build/\"]\n")
		gitCommitAll(t, repo, "baseline with exempt config")

		sid := "sty_ccc33333"
		mustWrite(t, filepath.Join(repo, "build", "x.txt"), "artifact\n")
		gitCommitAll(t, repo, "build artifact ("+sid+")")

		out, err := runCheckScript(t, scriptPath, repo, sid)
		if err != nil {
			t.Fatalf("edit_exempt_paths build/ slice should exit 0: %v\n%s", err, out)
		}
		if !strings.Contains(out, "substrate-only slice confirmed") {
			t.Errorf("want confirm line, got:\n%s", out)
		}
	})

	// Fresh-init default list includes .gitignore (sty_f115e6bf AC2): a slice
	// that is only the managed .gitignore path passes substrate-only close.
	t.Run("fresh-init default exempts gitignore slice", func(t *testing.T) {
		repo := t.TempDir()
		gitInit(t, repo)
		mustWrite(t, filepath.Join(repo, "README.md"), "baseline\n")
		mustWrite(t, filepath.Join(repo, ".satelle", "satelle.toml"),
			"[gate]\nedit_exempt_paths = [\".satelle/\", \".gitignore\"]\n")
		gitCommitAll(t, repo, "baseline with fresh-init exempt")

		sid := "sty_ddd44444"
		mustWrite(t, filepath.Join(repo, ".gitignore"), "# managed\n.satelle/local.toml\n")
		gitCommitAll(t, repo, "gitignore converge ("+sid+")")

		out, err := runCheckScript(t, scriptPath, repo, sid)
		if err != nil {
			t.Fatalf("fresh-init .gitignore slice should exit 0: %v\n%s", err, out)
		}
		if !strings.Contains(out, "substrate-only slice confirmed") {
			t.Errorf("want confirm line, got:\n%s", out)
		}
	})
}

// extractShippedSubstrateOnlyCheck reads the embedded skill source from the
// repo tree and returns the first ```check fence — the bytes that ship in the
// binary. Tests the real allow-list, not a re-derived copy.
func extractShippedSubstrateOnlyCheck(t *testing.T) string {
	t.Helper()
	src := filepath.Join(repoRootForTest(), "internal", "config", "substrate", "skills", "satelle-substrate-only-check.md")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read shipped skill: %v", err)
	}
	lines := strings.Split(string(body), "\n")
	in := false
	var out []string
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(trim, "```") {
				info := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
				if info == "check" || strings.HasPrefix(info, "check ") {
					in = true
				}
			}
			continue
		}
		if strings.HasPrefix(trim, "```") {
			break
		}
		out = append(out, ln)
	}
	s := strings.TrimSpace(strings.Join(out, "\n"))
	if s == "" {
		t.Fatal("shipped satelle-substrate-only-check carries no ```check block")
	}
	if !strings.Contains(s, `allow='\.satelle/|docs/|\.gitignore$|\.claude/|\.grok/'`) {
		t.Fatalf("shipped allow-list missing managed footprint:\n%s", s)
	}
	return s
}

func runCheckScript(t *testing.T, scriptPath, repo, sid string) (string, error) {
	t.Helper()
	payload := `{"story":{"id":"` + sid + `"},"from":"in_progress","to":"done"}`
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(payload)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
