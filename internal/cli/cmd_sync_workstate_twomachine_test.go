package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// twoMachineRoute is the fixture lifecycle: backlog → in_progress → done.
var twoMachineRoute = map[string]string{
	"done": "[meta]\nname = \"done\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" +
		"[\"*\"]\nobligations = [\"raised\", \"coded\", \"closed\"]\n",
	"step": "[meta]\nname = \"step\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"fixture\"\n\n" +
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n" +
		"[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\nrequires = [\"raised\"]\n\n" +
		"[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"coded\"]\n",
}

// machine is one checkout of the project: its own repo, database and app.
type machine struct {
	repo string
	db   *store.DB
	app  *app.App
}

func newMachine(t *testing.T, git bool) *machine {
	t.Helper()
	repo := t.TempDir()
	if git {
		for _, args := range [][]string{
			{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		} {
			c := exec.Command("git", args...)
			c.Dir = repo
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
			c := exec.Command("git", args...)
			c.Dir = repo
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(ledgerPushToml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt := t.TempDir()
	db, err := store.Open(filepath.Join(rt, config.DefaultDBName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &machine{repo: repo, db: db, app: &app.App{
		Config: cfg, RepoRoot: repo, RuntimeDir: rt,
		DBPath: filepath.Join(rt, config.DefaultDBName), Store: db,
	}}
}

// run invokes a work-state sync verb's RunE-level function against this machine.
func (m *machine) run(t *testing.T, fn func(*cobra.Command) error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.WithValue(context.Background(), appCtxKey{}, m.app))
	if err := fn(cmd); err != nil {
		t.Fatalf("sync verb: %v\n%s", err, out.String())
	}
}

func (m *machine) push(t *testing.T, server string) {
	t.Helper()
	m.run(t, func(c *cobra.Command) error { return runSyncWorkstatePush(c, server, false, false) })
}

func (m *machine) pull(t *testing.T, server string) {
	t.Helper()
	m.run(t, func(c *cobra.Command) error { return runSyncWorkstatePull(c, server, false, false, false) })
}

// pushInGateGater attaches a document and pushes this machine's work-state from
// inside the gate window of every edge into `done` — the window in which the
// transition's own rows are not yet written. Everything else passes at once.
type pushInGateGater struct {
	t      *testing.T
	m      *machine
	server string
}

func (g pushInGateGater) Gate(ctx context.Context, item workitem.Item, to string) (verb.GateDecision, error) {
	if to == "done" {
		if _, _, err := verb.AttachItemDoc(ctx, item, "mid-gate", "output", "attached while gated"); err != nil {
			return verb.GateDecision{}, err
		}
		time.Sleep(5 * time.Millisecond) // time-subject: timestamp separation, so the mid-gate attachment predates the push cursor
		g.m.push(g.t, g.server)
		time.Sleep(5 * time.Millisecond) // time-subject: timestamp separation, so the transition commit stamps after the push
	}
	return verb.GateDecision{Gated: false}, nil
}

func ledgerRowsOf(t *testing.T, m *machine, storyID, kind string) map[string]ledger.Entry {
	t.Helper()
	es, err := m.db.Ledger.ListByStory(context.Background(), storyID, kind)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ledger.Entry{}
	for _, e := range es {
		out[e.ID] = e
	}
	return out
}

func entryIDs(m map[string]ledger.Entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestTwoMachineStoryArrivesWithTransitionsAndChangeRecords pins sty_4a31e1ed
// AC5: a story driven to done on machine A, with a push landing inside a gate
// window, shows on machine B after a pull with the same status_transition rows
// (every from→to, same ids) and change_record rows (same ids, same head_sha).
func TestTwoMachineStoryArrivesWithTransitionsAndChangeRecords(t *testing.T) {
	withVerbWiring(t)
	ts, _ := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")

	a := newMachine(t, true)
	b := newMachine(t, false)

	// Machine A: wire the verb layer to its store, in its (git) tree.
	cwd, _ := os.Getwd()
	if err := os.Chdir(a.repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	wfDir := filepath.Join(t.TempDir(), "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range twoMachineRoute {
		if err := os.WriteFile(filepath.Join(wfDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatalf("sync workflows: %v", err)
	}
	verb.SetWorkItemStore(a.db.Stories)
	verb.SetLedgerStore(a.db.Ledger)
	verb.SetTxRunner(a.db.InTx)
	verb.SetDocIndexStore(a.db.DocIndex)
	verb.SetLeaseStore(a.db.Leases)
	verb.SetStoryDir(filepath.Join(a.repo, "stories"))
	// Only the gater is wired: this test drives the verb layer on its own
	// stores, with no reviewer engine behind the other seams.
	verb.SetTransitionGater(pushInGateGater{t: t, m: a, server: ts.URL})

	dispatch := func(name string, req map[string]any) workitem.Item {
		t.Helper()
		raw, _ := json.Marshal(req)
		resp, err := verb.Dispatch(context.Background(), name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var it workitem.Item
		if err := json.Unmarshal(resp, &it); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		return it
	}
	it := dispatch("story-create", map[string]any{
		"title": "two machines", "body": "goal", "acceptance_criteria": "1. ok",
		"category": "feature", "tags": []string{"workflow:cr-wf"},
	})
	a.push(t, ts.URL) // an early push, so the cursor is live before the gated edge
	it = dispatch("story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	it = dispatch("story-set", map[string]any{"id": it.ID, "status": "done"})
	if it.Status != "done" {
		t.Fatalf("machine A story is %s, want done", it.Status)
	}
	a.push(t, ts.URL)

	wantTransitions := ledgerRowsOf(t, a, it.ID, ledger.KindStatusTransition)
	wantChanges := ledgerRowsOf(t, a, it.ID, ledger.KindChangeRecord)
	if len(wantTransitions) < 2 || len(wantChanges) < 2 {
		t.Fatalf("machine A recorded %d transitions and %d change records, want >=2 each",
			len(wantTransitions), len(wantChanges))
	}

	b.pull(t, ts.URL)

	gotItem, err := b.db.Stories.Get(context.Background(), it.ID)
	if err != nil || gotItem.Status != "done" {
		t.Fatalf("machine B story = %+v, %v; want status done", gotItem, err)
	}
	gotTransitions := ledgerRowsOf(t, b, it.ID, ledger.KindStatusTransition)
	if got, want := entryIDs(gotTransitions), entryIDs(wantTransitions); !slices.Equal(got, want) {
		t.Errorf("status_transition ids on B = %v, want %v", got, want)
	}
	for id, w := range wantTransitions {
		if g, ok := gotTransitions[id]; ok && g.Body != w.Body {
			t.Errorf("transition %s on B reads %q, A has %q", id, g.Body, w.Body)
		}
	}
	gotChanges := ledgerRowsOf(t, b, it.ID, ledger.KindChangeRecord)
	if got, want := entryIDs(gotChanges), entryIDs(wantChanges); !slices.Equal(got, want) {
		t.Errorf("change_record ids on B = %v, want %v", got, want)
	}
	head := func(e ledger.Entry) string {
		var p struct {
			HeadSHA string `json:"head_sha"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		return p.HeadSHA
	}
	for id, w := range wantChanges {
		if head(w) == "" {
			t.Errorf("change_record %s on A carries no head_sha", id)
		}
		if g, ok := gotChanges[id]; ok && head(g) != head(w) {
			t.Errorf("change_record %s head_sha on B = %q, A has %q", id, head(g), head(w))
		}
	}
}
