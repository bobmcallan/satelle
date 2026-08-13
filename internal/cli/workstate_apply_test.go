package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestWorkstateApplyEnabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())

	local := config.Config{Sync: map[string]string{"stories": "local", "executions": "local", "ledger": "local"}}
	ok, err := workstateApplyEnabled(local)
	if err != nil || ok {
		t.Fatalf("all local: ok=%v err=%v", ok, err)
	}

	personal := config.Config{
		Sync:   map[string]string{"all": "personal"},
		Hosted: config.HostedConfig{Server: "https://h.example", Project: "p"},
	}
	ok, err = workstateApplyEnabled(personal)
	if err != nil || ok {
		t.Fatalf("personal without cred: ok=%v err=%v", ok, err)
	}

	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: "https://h.example", AccessToken: "a", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	ok, err = workstateApplyEnabled(personal)
	if err != nil || !ok {
		t.Fatalf("personal with cred: ok=%v err=%v", ok, err)
	}
}

func TestWorkstateApplyEnabledExplicitAreas(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: "https://h.example", AccessToken: "a", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Sync:   map[string]string{"stories": "personal", "executions": "local", "ledger": "local"},
		Hosted: config.HostedConfig{Server: "https://h.example", Project: "p"},
	}
	ok, err := workstateApplyEnabled(cfg)
	if err != nil || !ok {
		t.Fatalf("stories personal: ok=%v err=%v", ok, err)
	}
}

func TestWireWorkstateApplierLocalDoesNotFire(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	var n int
	verb.SetWorkstateApplier(func(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
		n++
	})
	t.Cleanup(verb.ClearWorkstateApplier)
	wireWorkstateApplier(&app.App{Config: config.Config{
		Sync: map[string]string{"stories": "local", "executions": "local", "ledger": "local"},
	}})
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
	})
	body, _ := json.Marshal(map[string]any{"title": "LocalOnly"})
	if _, err := verb.Dispatch(context.Background(), "story-create", body); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("local config still fired applier %d times", n)
	}
}

func TestApplyWorkstateNowPostsAndSurvives500(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())

	var posts int
	var sawConfig, sawDocs bool
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/config") {
			sawConfig = true
		}
		if strings.Contains(r.URL.Path, "/documents") {
			sawDocs = true
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workstate") {
			posts++
			if status != http.StatusOK {
				http.Error(w, "nope", status)
				return
			}
			_, _ = w.Write([]byte(`{"items":1,"ledger":0}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Sync:   map[string]string{"all": "personal"},
		Hosted: config.HostedConfig{Server: ts.URL, Project: "probe"},
	}
	a := &app.App{Config: cfg}

	var logs []string
	workstateApplyLog = func(s string) { logs = append(logs, s) }
	t.Cleanup(func() { workstateApplyLog = nil })

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
		verb.ClearWorkstateApplier()
	})

	wireWorkstateApplier(a)
	body, err := json.Marshal(map[string]any{"title": "ApplyMe"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := verb.Dispatch(context.Background(), "story-create", body)
	if err != nil {
		t.Fatalf("create on 200: %v", err)
	}
	var created workitem.Item
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || posts != 1 {
		t.Fatalf("id=%s posts=%d", created.ID, posts)
	}
	if sawConfig || sawDocs {
		t.Fatal("mutate apply hit config/documents")
	}

	status = http.StatusInternalServerError
	logs = nil
	body, _ = json.Marshal(map[string]any{"title": "ApplyFail"})
	raw, err = verb.Dispatch(context.Background(), "story-create", body)
	if err != nil {
		t.Fatalf("create must succeed on apply 500: %v", err)
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatal("row missing after apply 500")
	}
	if got, _ := db.Stories.Get(context.Background(), created.ID); got.Title != "ApplyFail" {
		t.Fatalf("store after 500: %+v", got)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "workstate apply") {
		t.Fatalf("expected apply log, got %v", logs)
	}
}
