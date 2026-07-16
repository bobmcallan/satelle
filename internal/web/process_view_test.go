package web_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/web"
)

// TestProcessViewProvenanceAndBindings (sty_ba0eb5c6): home + workflow fragment
// show provenance chips; expand carries agent bindings; disk edit → edited
// without restart.
func TestProcessViewProvenanceAndBindings(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := t.TempDir()
	data := filepath.Join(repo, ".satelle")
	for _, k := range []string{"workflows", "skills", "principles"} {
		if err := os.MkdirAll(filepath.Join(data, k), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "agents.toml"), []byte(
		"[reviewer]\nmodel = \"m-reviewer\"\n[executor]\nmodel = \"m-executor\"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	wfBody := "---\nname: pv-wf\napplies_to: [\"*\"]\n---\n\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  plan [agent=executor]\n  done [shape=Msquare]\n  backlog -> plan [agent=reviewer, prompt=\"@skill:satelle-story-intent-review\"]\n  plan -> done\n}\n```\n"
	if err := os.WriteFile(filepath.Join(data, "workflows", "pv-wf.md"), []byte(wfBody), 0o644); err != nil {
		t.Fatal(err)
	}
	skillBody := "---\nname: local-skill\ntype: skill\n---\n\n# Local skill\n\nbody\n"
	if err := os.WriteFile(filepath.Join(data, "skills", "local-skill.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Edited override of an embedded skill (forces provenance:edited).
	var embSkill, embBody string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" {
			embSkill, embBody = d.Name, d.Body
			break
		}
	}
	if embSkill == "" {
		t.Fatal("need an embedded skill")
	}
	editedPath := filepath.Join(data, "skills", embSkill+".md")
	if err := os.WriteFile(editedPath, []byte(embBody+"\n\noperator edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	verb.SetDataDir(data)
	dirs := map[string]string{
		"workflows":  filepath.Join(data, "workflows"),
		"skills":     filepath.Join(data, "skills"),
		"principles": filepath.Join(data, "principles"),
	}
	if _, err := db.DocIndex.Sync(context.Background(), dirs, time.Now()); err != nil {
		t.Fatal(err)
	}
	a := &app.App{RepoRoot: repo, DataDir: data, DBPath: filepath.Join(data, "satelle.db"), Store: db}
	srv := httptest.NewServer(web.Build(a))
	t.Cleanup(func() {
		srv.Close()
		db.Close()
		verb.SetWorkItemStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetDocIndexStore(nil)
		verb.SetLeaseStore(nil)
		verb.SetDataDir("")
	})

	code, page := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("home status %d", code)
	}
	for _, want := range []string{"provenance:edited", "pv-wf", "local-skill", embSkill} {
		if !strings.Contains(page, want) {
			t.Errorf("home missing %q", want)
		}
	}
	// Authored local skill should be labeled authored.
	if !strings.Contains(page, "provenance:authored") {
		t.Errorf("home missing provenance:authored for local-skill:\n%s", page[:min(2500, len(page))])
	}

	code, frag := get(t, srv.URL+"/fragment/workflow/pv-wf")
	if code != 200 {
		t.Fatalf("fragment status %d", code)
	}
	if !strings.Contains(frag, "provenance:") {
		t.Errorf("workflow expand missing provenance chip:\n%s", frag)
	}
	if !strings.Contains(frag, "wf-agent-badge") && !strings.Contains(frag, "@executor") {
		t.Errorf("workflow expand missing agent binding badge:\n%s", frag)
	}
	if !strings.Contains(frag, `data-state="plan"`) {
		t.Errorf("workflow expand missing plan node:\n%s", frag)
	}

	code, docp := get(t, srv.URL+"/doc/skills/"+embSkill)
	if code != 200 {
		t.Fatalf("doc page status %d", code)
	}
	if !strings.Contains(docp, "provenance:edited") {
		t.Errorf("doc page missing provenance:edited:\n%s", docp)
	}
	if !strings.Contains(docp, "source:") {
		t.Errorf("doc page missing source:\n%s", docp)
	}

	// AC3: further edit under same server → still edited (no restart).
	if err := os.WriteFile(editedPath, []byte(embBody+"\n\noperator edit again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"skills": dirs["skills"]}, time.Now()); err != nil {
		t.Fatal(err)
	}
	code, page = get(t, srv.URL+"/")
	if code != 200 {
		t.Fatal(code)
	}
	if !strings.Contains(page, "provenance:edited") {
		t.Errorf("after re-edit, home must still show provenance:edited:\n%s", page[:min(2500, len(page))])
	}
}
