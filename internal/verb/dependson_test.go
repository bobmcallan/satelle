package verb_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

func mkDep(t *testing.T, req map[string]any) (string, error) {
	t.Helper()
	req["title"] = "x"
	raw, err := dispatchRaw(t, "story-create", req)
	if err != nil {
		return "", err
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	return it.ID, nil
}

func addTags(t *testing.T, id string, tags ...string) error {
	t.Helper()
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "add_tags": tags})
	return err
}

func mustContain(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a refusal containing %v, got none", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("refusal %q must contain %q", err, w)
		}
	}
}

func TestDependsOnAtCreateAndSet(t *testing.T) {
	wire(t)
	parent, _ := mkDep(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
	a, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	b, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	c, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	outside, _ := mkDep(t, map[string]any{"tags": []string{"epic:other"}})

	// Repeated depends-on tags, each a story in the set, are accepted at create.
	if _, err := mkDep(t, map[string]any{"tags": []string{"epic:t", "depends-on:" + a, "depends-on:" + b}}); err != nil {
		t.Fatalf("repeated valid depends-on refused: %v", err)
	}

	// Not a story, and not an id at all.
	_, err := mkDep(t, map[string]any{"tags": []string{"epic:t", "depends-on:sty_nope"}})
	mustContain(t, err, "depends-on:sty_nope", "does not name a story")

	// A story outside the epic set: distinct message from not-a-story.
	_, err = mkDep(t, map[string]any{"tags": []string{"epic:t", "depends-on:" + outside}})
	mustContain(t, err, "depends-on:"+outside, "leaves the epic set")
	if strings.Contains(err.Error(), "does not name a story") {
		t.Errorf("an existing story outside the set must not read as not-a-story: %v", err)
	}

	// The parent need not carry the theme tag when it is parent_id.
	plain, _ := mkDep(t, map[string]any{"category": "fix"})
	if _, err := mkDep(t, map[string]any{"parent_id": plain, "tags": []string{"depends-on:" + plain}}); err != nil {
		t.Fatalf("depends-on the parent_id story refused: %v", err)
	}

	// No epic set at all.
	_, err = mkDep(t, map[string]any{"tags": []string{"depends-on:" + a}})
	mustContain(t, err, "depends-on:"+a, "no epic set")

	// Set: self-edge, 2-cycle, 3-cycle, and the accepted forward edges.
	mustContain(t, addTags(t, a, "depends-on:"+a), a+" depends on itself")
	if err := addTags(t, b, "depends-on:"+a); err != nil {
		t.Fatalf("b -> a: %v", err)
	}
	mustContain(t, addTags(t, a, "depends-on:"+b), "cycle", a+" -> "+b+" -> "+a)
	if err := addTags(t, c, "depends-on:"+b); err != nil {
		t.Fatalf("c -> b: %v", err)
	}
	mustContain(t, addTags(t, a, "depends-on:"+c), "cycle", a+" -> "+c+" -> "+b+" -> "+a)
	mustContain(t, addTags(t, a, "depends-on:"+outside), "depends-on:"+outside, "leaves the epic set")
	if err := addTags(t, a, "depends-on:"+parent); err != nil {
		t.Fatalf("a -> epic parent: %v", err)
	}
}

// AC1/AC3: depends-on:<repo>/<id> naming one other registered repo is accepted at
// create and at set, and a story whose only dependency is cross-repo needs no
// epic set.
func TestDependsOnCrossRepoAccepted(t *testing.T) {
	wire(t)
	verb.StubSiblingRegistry(t, map[string][]string{"satelle-server": {t.TempDir()}}, t.TempDir())
	const tag = "depends-on:satelle-server/sty_620c56ea"

	if _, err := mkDep(t, map[string]any{"tags": []string{tag}}); err != nil {
		t.Fatalf("cross-repo depends-on refused at create (no epic set needed): %v", err)
	}
	id, _ := mkDep(t, map[string]any{})
	if err := addTags(t, id, tag); err != nil {
		t.Fatalf("cross-repo depends-on refused at set: %v", err)
	}
	// A same-repo target keeps its own rule beside a cross-repo one.
	local, _ := mkDep(t, map[string]any{"category": "fix"})
	_, err := mkDep(t, map[string]any{"tags": []string{tag, "depends-on:" + local}})
	mustContain(t, err, "depends-on:"+local, "no epic set")
}

// AC2: each way a cross-repo tag can be wrong is refused, naming the tag.
func TestDependsOnCrossRepoRefusals(t *testing.T) {
	wire(t)
	main, wt := gitRepoWithWorktree(t)
	verb.StubSiblingRegistry(t, map[string][]string{
		"satelle-server": {t.TempDir()},
		"dup":            {t.TempDir(), t.TempDir()},
		"proj":           {config.CanonicalRepoRoot(main)},
	}, wt) // the invoking tree is a worktree of "proj"

	for _, tc := range []struct{ tag, want string }{
		{"depends-on:ghost/sty_1", "not in the workspace registry"},
		{"depends-on:dup/sty_1", "ambiguous repo name dup"},
		{"depends-on:proj/sty_1", "is this repo"},
		{"depends-on:satelle-server/nope", "does not name a story id"},
	} {
		_, err := mkDep(t, map[string]any{"tags": []string{tc.tag}})
		mustContain(t, err, tc.tag, tc.want)
		id, _ := mkDep(t, map[string]any{})
		mustContain(t, addTags(t, id, tc.tag), tc.tag, tc.want)
	}
}

// AC3: a cross-repo target is outside the epic-set and cycle checks; the local
// edges beside it are still judged.
func TestDependsOnCrossRepoSkipsEpicSetAndCycleChecks(t *testing.T) {
	wire(t)
	verb.StubSiblingRegistry(t, map[string][]string{"satelle-server": {t.TempDir()}}, t.TempDir())
	const cross = "depends-on:satelle-server/sty_620c56ea"
	a, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	b, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})

	if err := addTags(t, a, cross); err != nil {
		t.Fatalf("a -> cross-repo target: %v", err)
	}
	if err := addTags(t, b, cross, "depends-on:"+a); err != nil {
		t.Fatalf("b -> cross-repo target and a: %v", err)
	}
	mustContain(t, addTags(t, a, "depends-on:"+b), "cycle", a+" -> "+b+" -> "+a)
}

// gitRepoWithWorktree makes a satelle-governed git repo and a linked worktree of it.
func gitRepoWithWorktree(t *testing.T) (main, wt string) {
	t.Helper()
	base := t.TempDir()
	main = filepath.Join(base, "proj")
	if err := os.MkdirAll(filepath.Join(main, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt = filepath.Join(base, "wt")
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
		{"worktree", "add", "-q", "-b", "wt", wt, "HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = main
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return main, wt
}
