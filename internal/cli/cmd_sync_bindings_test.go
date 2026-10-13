package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// fakeBindingsServer is the hosted side a `sync bindings` verb talks to: the
// workspace list, the location registry, and ONE publish-catalog path
// (workflows/agents.toml). It records every request so a test can prove what
// left the machine, and that nothing did.
type fakeBindingsServer struct {
	mu       sync.Mutex
	requests []string
	putBody  []byte
	putKind  string
	putHdr   http.Header
	stored   []byte // catalog content served on GET (pre-seeded or from PUT)
	version  int
}

func newFakeBindingsServer(t *testing.T) (*httptest.Server, *fakeBindingsServer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hosted.LocationStatePathOverride = filepath.Join(t.TempDir(), "location-state.json")
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })
	f := &fakeBindingsServer{}
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
	}
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
			{"id": "ws-team", "kind": "team", "name": "Acme"},
		})
	})
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/v1/workspaces/ws-team/published/workflows/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			f.putBody = body
			f.putKind = r.URL.Query().Get("kind")
			f.putHdr = r.Header.Clone()
			created := string(f.stored) != string(body)
			if created {
				f.version++
				f.stored = body
			}
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"path": "workflows/agents.toml", "kind": f.putKind, "version": f.version, "created": created})
		case http.MethodGet:
			if f.stored == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("X-Satelle-Publish-Version", itoa(f.version))
			w.Header().Set("X-Satelle-Publish-Kind", "agents")
			w.Header().Set("X-Satelle-Publisher-Id", "user-1")
			_, _ = w.Write(f.stored)
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func (f *fakeBindingsServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

const secretAgentsToml = `[executor]
role    = "agent"
command = "in-loop"

[reviewer]
role    = "reviewer"
command = "/opt/local/bin/claude -p --output-format json --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob"
model   = "opus"
env     = { ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}", ANTHROPIC_BASE_URL = "https://literal.example" }
settings = { env = { CLAUDE_TOKEN = "live-secret" }, model = "opus" }
`

// TestSyncBindingsPushPublishesFileBytesAndStampsLocation (AC1, AC4): what
// reaches the catalog is the file's own bytes — comments, ${VAR} references and
// blanks included; the request carries a valid x-satelle-location; the kind is
// agents; and the body loads.
func TestSyncBindingsPushPublishesFileBytesAndStampsLocation(t *testing.T) {
	ts, f := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	agentsPushRepo(t, "[hosted]\nworkspace = \"Acme\"\n", pushableAgents)

	cmd, buf := testCmd()
	if err := runSyncBindingsPush(cmd, ts.URL, "", false); err != nil {
		t.Fatalf("push: %v\n%s", err, buf.String())
	}
	body := string(f.putBody)
	if body != pushableAgents {
		t.Errorf("published body is not the file's bytes:\n%s", body)
	}
	if f.putKind != "agents" {
		t.Errorf("kind = %q, want agents", f.putKind)
	}
	loc := f.putHdr.Get(hosted.LocationHeader)
	if loc == "" || !hosted.ValidLocationID(loc) {
		t.Errorf("%s header = %q, want a valid location id", hosted.LocationHeader, loc)
	}
	// The published body loads as an agents layer.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, config.AgentsConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.AgentsRel), f.putBody, 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := config.LoadAgents(dir)
	if err != nil {
		t.Fatalf("published body does not load: %v\n%s", err, body)
	}
	if ac.Reviewer.Env["ANTHROPIC_AUTH_TOKEN"] != "${GLM_API_KEY}" || !strings.HasPrefix(ac.Reviewer.Command, "claude -p") {
		t.Errorf("loaded reviewer = %+v", ac.Reviewer)
	}
	if strings.Contains(strings.ToLower(buf.String()), "redact") {
		t.Errorf("push output still claims redaction: %s", buf.String())
	}
	// A second push of unchanged config is a no-op version-wise.
	cmd2, buf2 := testCmd()
	if err := runSyncBindingsPush(cmd2, ts.URL, "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf2.String(), "unchanged at v1") {
		t.Errorf("re-push should be unchanged: %s", buf2.String())
	}
}

// TestSyncBindingsUnboundIsNoOp (AC5): no team workspace → nothing to do,
// zero hosted requests, exit 0.
func TestSyncBindingsUnboundIsNoOp(t *testing.T) {
	ts, f := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	repo := syncConfigRepo(t, "") // no [hosted] workspace → personal → unbound for bindings
	writeRepoFile(t, repo, ".satelle/workflows/agents.toml", secretAgentsToml)
	pointAt(t, repo)

	for _, run := range []func(*testing.T) (string, error){
		func(t *testing.T) (string, error) {
			c, b := testCmd()
			err := runSyncBindingsPush(c, ts.URL, "", false)
			return b.String(), err
		},
		func(t *testing.T) (string, error) {
			c, b := testCmd()
			err := runSyncBindingsPull(c, ts.URL, "")
			return b.String(), err
		},
	} {
		out, err := run(t)
		if err != nil {
			t.Fatalf("unbound must not error: %v", err)
		}
		if !strings.Contains(out, "nothing to do") {
			t.Errorf("want no-op note, got %q", out)
		}
	}
	if n := f.count(); n != 0 {
		t.Fatalf("unbound repo contacted the server %d time(s): %v", n, f.requests)
	}
}

// TestSyncBindingsPullAppliesLayerAsPublished (AC2, AC3, AC8): a catalog entry
// lands as the workspace layer exactly as published, and the effective ladder
// then shows the repo winning field by field with the workspace named as the
// source of what it supplied. A blank the layer declares is "declared,
// unsatisfied" — it never shadows the machine's own value.
func TestSyncBindingsPullAppliesLayerAsPublished(t *testing.T) {
	ts, f := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	published := "[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\ntools = \"Read\"\nmodel = \"sonnet\"\nenv = { TOKEN = \"\", BASE_URL = \"${BASE_URL}\" }\nsettings = { apiKey = \"\", model = \"sonnet\" }\n\n[coder]\nrole = \"agent\"\ncommand = \"claude -p {system}\"\n"
	f.stored = []byte(published)
	f.version = 3
	repo := syncConfigRepo(t, "[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, repo, ".satelle/workflows/agents.toml", "[reviewer]\nrole = \"reviewer\"\nmodel = \"opus\"\n")
	pointAt(t, repo)

	cmd, buf := testCmd()
	if err := runSyncBindingsPull(cmd, ts.URL, ""); err != nil {
		t.Fatalf("pull: %v\n%s", err, buf.String())
	}
	dataDir := filepath.Join(repo, ".satelle")
	layer, err := os.ReadFile(config.WorkspaceAgentsPath(dataDir))
	if err != nil {
		t.Fatalf("workspace layer not written: %v", err)
	}
	if string(layer) != published {
		t.Fatalf("ingest must write the layer as published:\n%s", layer)
	}
	// The authored file is untouched.
	authored, _ := os.ReadFile(filepath.Join(dataDir, config.AgentsRel))
	if !strings.Contains(string(authored), `model = "opus"`) || strings.Contains(string(authored), "coder") {
		t.Fatalf("sync must never rewrite the authored agents.toml:\n%s", authored)
	}
	eff, err := config.LoadEffectiveAgents(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Agents.Reviewer.Model != "opus" {
		t.Errorf("repo field must win: %+v", eff.Agents.Reviewer)
	}
	if eff.Agents.Reviewer.Tools != "Read" || !strings.HasPrefix(eff.Agents.Reviewer.Command, "claude -p") {
		t.Errorf("workspace must fill blanks with its command: %+v", eff.Agents.Reviewer)
	}
	if eff.Provenance.Source("reviewer", "tools") != config.SourceWorkspace || eff.Provenance.Source("reviewer", "model") != config.SourceRepo {
		t.Errorf("provenance = %+v", eff.Provenance["reviewer"])
	}
	// AC3: the EFFECTIVE binding never carries a blank the layer declares — the
	// machine's own TOKEN (from its shell) is not shadowed by `TOKEN=` — while
	// the ${VAR} reference is carried for local resolution; the blank secret
	// setting does not ride into --settings.
	if _, shadowed := eff.Agents.Reviewer.Env["TOKEN"]; shadowed {
		t.Fatalf("a blank must not shadow the machine environment: %+v", eff.Agents.Reviewer.Env)
	}
	if eff.Agents.Reviewer.Env["BASE_URL"] != "${BASE_URL}" {
		t.Errorf("reference must be carried: %+v", eff.Agents.Reviewer.Env)
	}
	if _, ok := eff.Agents.Reviewer.Settings["apiKey"]; ok {
		t.Errorf("blank secret setting must not ride into --settings: %+v", eff.Agents.Reviewer.Settings)
	}
	if c, ok := eff.Agents.Agents["coder"]; !ok || !strings.HasPrefix(c.Command, "claude -p") {
		t.Errorf("workspace-only coder = %+v (ok=%v)", c, ok)
	}
	if !strings.Contains(buf.String(), "v3") || !strings.Contains(buf.String(), config.WorkspaceAgentsRel) {
		t.Errorf("pull output = %q", buf.String())
	}
}

// TestSyncBindingsPullRefusesMachineSpecificLayer (AC8): a catalog entry an older
// push left carrying absolute paths and env values is refused with the pointer to
// agents.local.toml — nothing is written, nothing altered, and the authored files
// are left as they were.
func TestSyncBindingsPullRefusesMachineSpecificLayer(t *testing.T) {
	ts, f := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	f.stored = []byte(secretAgentsToml)
	f.version = 2
	repo := agentsPushRepo(t, "[hosted]\nworkspace = \"Acme\"\n", pushableAgents)
	const local = "[reviewer]\ncommand = \"/opt/x/claude -p {system}\"\n"
	writeRepoFile(t, repo, ".satelle/workflows/agents.local.toml", local)

	cmd, buf := testCmd()
	err := runSyncBindingsPull(cmd, ts.URL, "")
	if err == nil {
		t.Fatalf("a machine-specific catalog entry was applied:\n%s", buf.String())
	}
	for _, want := range []string{"agents.toml [reviewer] command", "agents.toml [reviewer] env.ANTHROPIC_BASE_URL", ".satelle/workflows/agents.local.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
	dataDir := filepath.Join(repo, ".satelle")
	if _, serr := os.Stat(config.WorkspaceAgentsPath(dataDir)); serr == nil {
		t.Error("a refused layer must not be written")
	}
	if got, _ := os.ReadFile(filepath.Join(dataDir, config.AgentsRel)); string(got) != pushableAgents {
		t.Errorf("agents.toml was altered:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dataDir, "workflows", config.LocalAgentsName)); string(got) != local {
		t.Errorf("agents.local.toml was altered:\n%s", got)
	}
}

// TestSyncBindingsPullNothingPublished: no catalog entry → a note, no file.
func TestSyncBindingsPullNothingPublished(t *testing.T) {
	ts, _ := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	repo := syncConfigRepo(t, "[hosted]\nworkspace = \"Acme\"\n")
	pointAt(t, repo)
	cmd, buf := testCmd()
	if err := runSyncBindingsPull(cmd, ts.URL, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "published no agents layer") {
		t.Errorf("out = %q", buf.String())
	}
	if _, err := os.Stat(config.WorkspaceAgentsPath(filepath.Join(repo, ".satelle"))); err == nil {
		t.Error("no layer must be written when nothing is published")
	}
}

// TestSyncBindingsPushDryRunContactsNothing: --dry-run prints the layer that would
// be published and makes no request; a layer that would be refused is refused the
// same way, and still no request is made.
func TestSyncBindingsPushDryRunContactsNothing(t *testing.T) {
	ts, f := newFakeBindingsServer(t)
	seedCred(t, ts.URL)
	repo := agentsPushRepo(t, "[hosted]\nworkspace = \"Acme\"\n", pushableAgents)
	cmd, buf := testCmd()
	if err := runSyncBindingsPush(cmd, ts.URL, "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ANTHROPIC_AUTH_TOKEN") || !strings.Contains(buf.String(), "${GLM_API_KEY}") {
		t.Errorf("dry-run must print the layer: %s", buf.String())
	}

	writeRepoFile(t, repo, ".satelle/workflows/agents.toml", secretAgentsToml)
	cmd2, buf2 := testCmd()
	err := runSyncBindingsPush(cmd2, ts.URL, "", true)
	if err == nil || !strings.Contains(err.Error(), "agents.local.toml") {
		t.Fatalf("dry-run must report what would be refused and fail: err=%v\n%s", err, buf2.String())
	}
	if n := f.count(); n != 0 {
		t.Fatalf("dry-run contacted the server %d time(s)", n)
	}
}

// TestAgentValidateNamesWorkspaceSource (AC2 inspection): with a workspace
// layer present, `satelle agent validate` reports it and attributes the fields
// it supplied to "workspace".
func TestAgentValidateNamesWorkspaceSource(t *testing.T) {
	repo := tempRepo(t)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte("[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\nmodel = \"opus\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, config.WorkspaceAgentsName), []byte("[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p --append-system-prompt {system} --model {model}\"\ntools = \"Read,Grep,Glob\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runRoot(t, "agent", "validate")
	if !strings.Contains(out, "Workspace bindings layer: "+config.WorkspaceAgentsRel+" (present") {
		t.Errorf("validate should report the layer:\n%s", out)
	}
	if !strings.Contains(out, "← workspace (.satelle/workflows/agents.workspace.toml)") {
		t.Errorf("validate should attribute workspace-supplied fields:\n%s", out)
	}
	if !strings.Contains(out, `model = "opus" ← repo (.satelle/workflows/agents.toml)`) {
		t.Errorf("validate should attribute repo fields:\n%s", out)
	}
}
