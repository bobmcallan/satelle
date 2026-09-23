package verb

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/compact"
)

// stubOffloader is the minimal compact.Offloader a test needs: content-addressed,
// no retrieval required since this test only checks that an offload happened.
type stubOffloader struct{}

func (stubOffloader) Put(content []byte) (string, error) {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

// TestGitDiffSinceForcesABPrefixUnderMnemonicPrefix (sty_75b76691 AC2): a repo
// with `git config diff.mnemonicPrefix true` set locally emits `diff --git
// c/X w/Y` and `--- c/X` / `+++ w/Y` by default. compact.CompactPatch keys its
// noise/lockfile offload off the a/ b/ shape, so gitDiffSince must force it
// back regardless of that config — otherwise a noise section silently stays
// inline on any machine with that setting.
func TestGitDiffSinceForcesABPrefixUnderMnemonicPrefix(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "t@t")
	run("git", "config", "user.name", "t")
	run("git", "config", "diff.mnemonicPrefix", "true")

	// Long enough lines that the offload marker (which carries a 64-char sha256
	// hex hash) is still shorter than the hunk it replaces — a short hunk would
	// legitimately stay inline under AC3's shrink-or-keep rule.
	before := "github.com/example/one v1.0.0 h1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n" +
		"github.com/example/two v2.0.0 h1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=\n" +
		"github.com/example/three v3.0.0 h1:ccccccccccccccccccccccccccccccccccccccccccccccc=\n"
	after := "github.com/example/one v1.0.1 h1:dddddddddddddddddddddddddddddddddddddddddddddddd=\n" +
		"github.com/example/two v2.0.1 h1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee=\n" +
		"github.com/example/three v3.0.1 h1:fffffffffffffffffffffffffffffffffffffffffffffff=\n"

	goSum := filepath.Join(dir, "go.sum")
	if err := os.WriteFile(goSum, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "go.sum")
	run("git", "commit", "-m", "init")

	if err := os.WriteFile(goSum, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}

	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	headSHA := strings.TrimSpace(string(head))

	_, _, patch, err := gitDiffSince(dir, headSHA, true)
	if err != nil {
		t.Fatalf("gitDiffSince: %v", err)
	}

	if !strings.Contains(patch, "diff --git a/go.sum b/go.sum") {
		t.Errorf("gitDiffSince did not force a/ b/ headers despite diff.mnemonicPrefix=true:\n%s", patch)
	}
	if strings.Contains(patch, "c/go.sum") || strings.Contains(patch, "w/go.sum") {
		t.Errorf("gitDiffSince leaked mnemonic c/ w/ prefixes:\n%s", patch)
	}

	out := compact.CompactPatch(patch, []string{"go.sum"}, stubOffloader{})
	if strings.Contains(out, "v1.0.1") {
		t.Errorf("CompactPatch left the go.sum hunk body inline given a forced a/ b/ header:\n%s", out)
	}
	if !strings.Contains(out, "diff --git a/go.sum b/go.sum") {
		t.Errorf("CompactPatch dropped the go.sum file header:\n%s", out)
	}
	if !strings.Contains(out, "@@ offloaded ") {
		t.Errorf("go.sum section was not offloaded (noise match failed on the forced a/ b/ header):\n%s", out)
	}
}
