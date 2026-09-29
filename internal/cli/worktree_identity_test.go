package cli

// A git worktree is the repository, not a project of its own (sty_cd219594).
//
// These tests drive the real verbs (init, workspace add/list/partitions, story
// create/route) against a real `git worktree add`, a real mirror store and the
// real web landing handler, so the registry, the partition and the view are the
// ones an operator sees.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/web"
)

// wtFixture is a satelle-governed main tree, its linked worktree, and a live
// mirror behind an ingest endpoint on an isolated SATELLE_HOME.
type wtFixture struct {
	main, wt string
	canon    string // the main tree's canonical (symlink-resolved) root
	ms       *mirror.Store
	srv      *httptest.Server
	out      string // output of `satelle init` run in the worktree
}

// newWorktreeFixture builds the main tree (initialised, registered and seeded to
// the mirror) and a linked worktree of it that has NOT been touched by satelle
// yet. The process is left inside the main tree.
func newWorktreeFixture(t *testing.T) *wtFixture {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "")
	_ = os.Unsetenv(config.SessionEnv)
	t.Setenv("SATELLE_CONFIG", "")
	_ = os.Unsetenv("SATELLE_CONFIG")

	ms, err := mirror.Open(mirror.DefaultPath(config.GlobalDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Close() })
	srv := httptest.NewServer(web.NewMirror(ms).Handler)
	t.Cleanup(srv.Close)
	t.Setenv(EnvServerEndpoint, srv.URL)

	base := t.TempDir()
	main := filepath.Join(base, "proj")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		gitIn(t, main, c...)
	}
	wt := filepath.Join(base, "wt", "sty_deadbeef")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "worktree", "add", "-q", "-b", "sty_deadbeef", wt, "HEAD")

	if err := runInit(io.Discard, main, false, nil); err != nil {
		t.Fatalf("init main: %v", err)
	}
	writeOpenGateConfig(t, main)
	t.Chdir(main)
	if out, err := runRoot(t, "workspace", "add"); err != nil {
		t.Fatalf("workspace add (main): %v\n%s", err, out)
	}
	return &wtFixture{main: main, wt: wt, canon: config.CanonicalRepoRoot(main), ms: ms, srv: srv}
}

// writeOpenGateConfig lets a fixture repo create stories without the create gate.
func writeOpenGateConfig(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, ".satelle", "satelle.toml")
	if err := os.WriteFile(p, []byte("[review]\ngate_create = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initWorktree runs `satelle init` in the worktree, exactly as a worktree
// dispatch does, and records its output.
func (f *wtFixture) initWorktree(t *testing.T) {
	t.Helper()
	var out strings.Builder
	if err := runInit(&out, f.wt, false, nil); err != nil {
		t.Fatalf("init worktree: %v\n%s", err, out.String())
	}
	f.out = out.String()
	writeOpenGateConfig(t, f.wt)
}

// useWorktree exercises the worktree the way a dispatched agent does: from
// inside it, re-seed the mirror and write a story (which drains a snapshot).
func (f *wtFixture) useWorktree(t *testing.T) {
	t.Helper()
	t.Chdir(f.wt)
	if out, err := runRoot(t, "workspace", "add"); err != nil {
		t.Fatalf("workspace add (worktree): %v\n%s", err, out)
	}
	if out, err := runRoot(t, "story", "create", "--title", "from the worktree",
		"--body", "b", "--acceptance", "1. a", "--category", "chore"); err != nil {
		t.Fatalf("story create (worktree): %v\n%s", err, out)
	}
}

func registry(t *testing.T) []string {
	t.Helper()
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	out := append([]string(nil), gc.Workspace.Repos...)
	sort.Strings(out)
	return out
}

func partitionDetails(t *testing.T, ms *mirror.Store) []mirror.PartitionDetail {
	t.Helper()
	d, err := ms.ListPartitionDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func partitionIdentity(t *testing.T, ms *mirror.Store, repoKey string) mirror.IdentityMeta {
	t.Helper()
	payload, err := ms.GetItem(context.Background(), repoKey, "identity", "meta")
	if err != nil {
		t.Fatalf("read identity of %s: %v", repoKey, err)
	}
	var id mirror.IdentityMeta
	if err := json.Unmarshal([]byte(payload), &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func landingBody(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %s\n%s", resp.Status, b)
	}
	return string(b)
}

// AC2: `satelle init` in a worktree registers nothing and says which repository
// it already is, naming the main tree.
func TestWorktreeInitReportsAlreadyRegisteredNamingMain(t *testing.T) {
	f := newWorktreeFixture(t)
	before := registry(t)
	if !reflect.DeepEqual(before, []string{f.canon}) {
		t.Fatalf("precondition: registry = %v, want just the main tree %q", before, f.canon)
	}

	f.initWorktree(t)

	if after := registry(t); !reflect.DeepEqual(after, before) {
		t.Errorf("init in a worktree changed the registry:\n before %v\n after  %v", before, after)
	}
	if !strings.Contains(f.out, "already registered") {
		t.Errorf("worktree init must report `already registered`:\n%s", f.out)
	}
	if !strings.Contains(f.out, f.canon) {
		t.Errorf("the already-registered line must name the main tree %q:\n%s", f.canon, f.out)
	}
	if strings.Contains(f.out, "+ workspace registry") {
		t.Errorf("worktree init must not register anything:\n%s", f.out)
	}
}

// AC3: a worktree never appears as a project — not in `workspace list`, not in the
// workspace view — however it is initialised and used.
func TestWorktreeNeverAppearsAsAProject(t *testing.T) {
	f := newWorktreeFixture(t)
	regBefore := registry(t)
	partsBefore := len(partitionDetails(t, f.ms))
	if viewBefore := landingBody(t, f.srv); !strings.Contains(viewBefore, "proj") {
		t.Fatalf("precondition: the view must list the main project:\n%s", viewBefore)
	}

	f.initWorktree(t)
	f.useWorktree(t)

	if got := len(registry(t)); got != len(regBefore) {
		t.Errorf("registry project count %d -> %d after a worktree was initialised and used", len(regBefore), got)
	}
	if got := len(partitionDetails(t, f.ms)); got != partsBefore {
		t.Errorf("workspace view project count %d -> %d after a worktree was initialised and used", partsBefore, got)
	}
	for _, r := range registry(t) {
		if strings.Contains(r, "sty_deadbeef") {
			t.Errorf("registry row %q names the worktree", r)
		}
	}
	view := landingBody(t, f.srv)
	if strings.Contains(view, "sty_deadbeef") {
		t.Errorf("the workspace view names the worktree:\n%s", view)
	}
	if !strings.Contains(view, "proj") {
		t.Errorf("the main project is no longer in the workspace view:\n%s", view)
	}
}

// AC4: seeding from a worktree leaves the partition naming the main tree — slug,
// path, project name and repo_key. It also pins sty_dfc9b100's relabel bug.
func TestWorktreeSnapshotKeepsPartitionIdentity(t *testing.T) {
	f := newWorktreeFixture(t)
	before := partitionDetails(t, f.ms)
	if len(before) != 1 {
		t.Fatalf("precondition: want one partition, got %+v", before)
	}
	if before[0].Slug != "proj" || before[0].Path != f.canon {
		t.Fatalf("precondition: partition = %q at %q, want proj at %q", before[0].Slug, before[0].Path, f.canon)
	}

	f.initWorktree(t)
	f.useWorktree(t)

	after := partitionDetails(t, f.ms)
	if len(after) != 1 {
		t.Fatalf("a worktree seed must not add a partition: %+v", after)
	}
	p := after[0]
	if p.RepoKey != before[0].RepoKey {
		t.Errorf("repo_key %q -> %q", before[0].RepoKey, p.RepoKey)
	}
	if p.Slug != "proj" {
		t.Errorf("slug = %q, want the main tree's %q (a worktree relabelled its parent's partition)", p.Slug, "proj")
	}
	if p.Path != f.canon {
		t.Errorf("path = %q, want the main tree %q", p.Path, f.canon)
	}
	id := partitionIdentity(t, f.ms, p.RepoKey)
	if id.ProjectName != "proj" || id.RepoRoot != f.canon {
		t.Errorf("identity meta = {%q, %q}, want {proj, %q}", id.ProjectName, id.RepoRoot, f.canon)
	}
}

// The mid-dispatch seat push is a SECOND snapshot builder, and it fires from inside
// a worktree on every throttled dispatch event. The mirror overwrites a partition's
// slug from whichever snapshot lands last, so a builder that slugs the raw root
// relabels the parent's partition even when every other sink is canonical. Found by
// the sink audit, not by the ACs' named paths.
func TestWorktreeMidDispatchPushKeepsPartitionSlug(t *testing.T) {
	f := newWorktreeFixture(t)
	f.initWorktree(t)
	t.Chdir(f.wt)

	a, err := app.Open()
	if err != nil {
		t.Fatalf("open the worktree's app: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if a.RepoRoot == f.canon {
		t.Fatalf("precondition: the app must be rooted in the worktree, got %q", a.RepoRoot)
	}
	snap, err := buildSeatSnapshot(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Slug != "proj" {
		t.Fatalf("seat snapshot slug = %q, want the main tree's %q", snap.Slug, "proj")
	}

	pushSeatSnapshot(a, f.srv.URL)

	parts := partitionDetails(t, f.ms)
	if len(parts) != 1 || parts[0].Slug != "proj" {
		t.Errorf("after a mid-dispatch push from the worktree the partitions are %+v, want one slugged %q", parts, "proj")
	}
}

// AC5: after a worktree registration, `workspace list` and `workspace partitions`
// name the same set of projects, and neither drops one that is still registered.
func TestWorktreeWorkspaceListAndPartitionsAgree(t *testing.T) {
	f := newWorktreeFixture(t)
	f.initWorktree(t)
	f.useWorktree(t)

	listOut, err := runRoot(t, "workspace", "list")
	if err != nil {
		t.Fatalf("workspace list: %v\n%s", err, listOut)
	}
	partsOut, err := runRoot(t, "workspace", "partitions")
	if err != nil {
		t.Fatalf("workspace partitions: %v\n%s", err, partsOut)
	}

	listed := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(listOut), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			listed[l] = true
		}
	}
	partitioned := map[string]bool{}
	lines := strings.Split(strings.TrimSpace(partsOut), "\n")
	for _, l := range lines[1:] { // the first line is the column header
		if fs := strings.Fields(l); len(fs) > 0 {
			partitioned[fs[len(fs)-1]] = true // PATH is the last column
		}
	}

	if !reflect.DeepEqual(listed, partitioned) {
		t.Errorf("workspace list and workspace partitions name different projects:\n list       %v\n partitions %v\n%s", listed, partitioned, partsOut)
	}
	if !listed[f.canon] || !partitioned[f.canon] {
		t.Errorf("the main tree %q is missing from list %v or partitions %v", f.canon, listed, partitioned)
	}
	for _, reg := range registry(t) {
		if !partitioned[reg] {
			t.Errorf("registered project %q is absent from workspace partitions", reg)
		}
	}
	for p := range listed {
		if strings.Contains(p, "sty_deadbeef") {
			t.Errorf("workspace list names the worktree: %q", p)
		}
	}
}

// AC7: a worktree derives its route from its OWN authored substrate, not the
// embedded default. The identity fix moved identity only; it must not have moved
// the data dir, or a worktree would silently derive the embedded route.
func TestWorktreeDerivesItsOwnAuthoredRoute(t *testing.T) {
	f := newWorktreeFixture(t)
	f.initWorktree(t)

	// Only the WORKTREE carries the authored six-step route; the main tree stays on
	// the embedded default, so the two routes differ iff the worktree reads its own.
	wf := filepath.Join(f.wt, ".satelle", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"done.toml": sixStepDone, "step.toml": sixStepCatalogue} {
		if err := os.WriteFile(filepath.Join(wf, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runRoot(t, "story", "create", "--title", "route probe", "--body", "b",
		"--acceptance", "1. a", "--category", "feature")
	if err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.ID == "" {
		t.Fatalf("parse story create: %v\n%s", err, out)
	}

	t.Chdir(f.wt)
	wtRoute, err := runRoot(t, "story", "route", created.ID)
	if err != nil {
		t.Fatalf("story route (worktree): %v\n%s", err, wtRoute)
	}
	for _, step := range []string{"backlog", "plan", "in_progress", "integration", "release", "done"} {
		if !strings.Contains(wtRoute, "**"+step+"**") {
			t.Errorf("worktree route has no %q step:\n%s", step, wtRoute)
		}
	}
	if !strings.Contains(wtRoute, "  6. **done**") {
		t.Errorf("worktree route is not the six-step feature route:\n%s", wtRoute)
	}

	t.Chdir(f.main)
	mainRoute, err := runRoot(t, "story", "route", created.ID)
	if err != nil {
		t.Fatalf("story route (main): %v\n%s", err, mainRoute)
	}
	if strings.Contains(mainRoute, "**plan**") {
		t.Errorf("control: the main tree has no authored route, yet derived a plan step:\n%s", mainRoute)
	}
}

const sixStepDone = `[meta]
name = "done"
type = "workflow"
scope = "project"

["*"]
obligations = ["raised", "readied", "coded", "integrated", "released", "closed"]
cancel = { state = "cancelled" }
`

const sixStepCatalogue = `[meta]
name = "step"
type = "workflow"
scope = "project"

[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
skills = ["coder"]
requires = ["readied"]

[integrated]
status = "integration"
agent = "executor"
skills = ["integrate"]
requires = ["coded"]

[released]
status = "release"
agent = "executor"
skills = ["release"]
requires = ["integrated"]

[closed]
status = "done"
terminal = true
requires = ["released"]
`
