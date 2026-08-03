package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

// seedCatalog writes a tuned catalog with [vars] secrets under SATELLE_HOME.
func seedCatalog(t *testing.T, home, body string) string {
	t.Helper()
	t.Setenv("SATELLE_HOME", home)
	path := filepath.Join(home, config.GlobalAgentsName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfilesPushRestoreRoundTripCleanHome(t *testing.T) {
	isolateUserHome(t)
	var stored []byte
	var putPath string
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		putPath = r.URL.Path
		stored, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"path":"agents.toml","created":true}`))
	})
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		if stored == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(stored)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r",
	}); err != nil {
		t.Fatalf("FileStore.Save: %v", err)
	}
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatalf("SaveGlobalHostedServer: %v", err)
	}

	homeA := t.TempDir()
	catalogBody := `# tuned catalog
[vars]
SECRET = "sk-live-secret"

[profiles.judge]
role = "reviewer"
command = "claude -p {system}"
`
	seedCatalog(t, homeA, catalogBody)

	// Snapshot a repo tree — must stay untouched.
	repo := t.TempDir()
	repoAgents := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	_ = os.MkdirAll(filepath.Dir(repoAgents), 0o755)
	_ = os.WriteFile(repoAgents, []byte("[executor]\ncommand = \"in-loop\"\n"), 0o644)
	before, _ := os.ReadFile(repoAgents)

	root := &cobra.Command{Use: "satelle"}
	agent := &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)

	// push from homeA
	t.Setenv("SATELLE_HOME", homeA)
	root.SetArgs([]string{"agent", "profiles", "push", "--server", ts.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("push: %v", err)
	}
	if putPath != "/api/v1/me/files/agents.toml" {
		t.Fatalf("PUT path = %q, want me/files (not projects)", putPath)
	}
	if strings.Contains(string(stored), "sk-live-secret") {
		t.Fatalf("pushed body must not contain vars secrets:\n%s", stored)
	}
	if !strings.Contains(string(stored), "[profiles.judge]") {
		t.Fatalf("pushed body must keep profiles:\n%s", stored)
	}

	// restore onto clean homeB
	homeB := t.TempDir()
	t.Setenv("SATELLE_HOME", homeB)
	root.SetArgs([]string{"agent", "profiles", "restore", "--server", ts.URL})
	// re-bind command tree for fresh execute
	root = &cobra.Command{Use: "satelle"}
	agent = &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)
	root.SetArgs([]string{"agent", "profiles", "restore", "--server", ts.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(homeB, config.GlobalAgentsName))
	if err != nil {
		t.Fatalf("restored file: %v", err)
	}
	if string(restored) != string(stored) {
		t.Fatalf("restored != pushed:\n%s\nvs\n%s", restored, stored)
	}
	after, _ := os.ReadFile(repoAgents)
	if string(after) != string(before) {
		t.Fatal("repo .satelle/ must be untouched by push/restore")
	}
}

func TestProfilesPushUnauthenticated(t *testing.T) {
	isolateUserHome(t)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	_ = config.SaveGlobalHostedServer(ts.URL)

	home := t.TempDir()
	seedCatalog(t, home, "[profiles.p]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n")
	t.Setenv("SATELLE_HOME", home)

	root := &cobra.Command{Use: "satelle"}
	agent := &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)
	root.SetArgs([]string{"agent", "profiles", "push", "--server", ts.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("want error when unauthorized")
	}
	if !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("error must name login: %v", err)
	}
}

func TestProfilesRestoreNonDestructive(t *testing.T) {
	isolateUserHome(t)
	body := []byte("[profiles.remote]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	_ = config.SaveGlobalHostedServer(ts.URL)

	home := t.TempDir()
	existing := "[profiles.local]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n"
	seedCatalog(t, home, existing)
	t.Setenv("SATELLE_HOME", home)

	root := &cobra.Command{Use: "satelle"}
	agent := &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)
	root.SetArgs([]string{"agent", "profiles", "restore", "--server", ts.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want refuse without --force, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(home, config.GlobalAgentsName))
	if string(got) != existing {
		t.Fatalf("existing catalog must be unchanged: %q", got)
	}

	// with --force
	root = &cobra.Command{Use: "satelle"}
	agent = &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)
	root.SetArgs([]string{"agent", "profiles", "restore", "--server", ts.URL, "--force"})
	if err := root.Execute(); err != nil {
		t.Fatalf("force restore: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(home, config.GlobalAgentsName))
	if string(got) != string(body) {
		t.Fatalf("force wrote %q", got)
	}
	bak, err := os.ReadFile(filepath.Join(home, config.GlobalAgentsName+".bak"))
	if err != nil {
		t.Fatalf("bak: %v", err)
	}
	if string(bak) != existing {
		t.Fatalf("bak = %q, want previous content", bak)
	}
}

func TestProfilesRestoreUnauthenticatedCleanHome(t *testing.T) {
	isolateUserHome(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	_ = config.SaveGlobalHostedServer(ts.URL)

	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	path := filepath.Join(home, config.GlobalAgentsName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition: catalog must not exist, stat=%v", err)
	}

	root := &cobra.Command{Use: "satelle"}
	agent := &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)
	root.SetArgs([]string{"agent", "profiles", "restore", "--server", ts.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("want login error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("catalog must still be absent after 401 restore, stat=%v", err)
	}
}

func TestProfilesRestoreUnauthenticatedExistingUnchanged(t *testing.T) {
	isolateUserHome(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	if err := (hosted.FileStore{}).Save(hosted.Credential{
		ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r",
	}); err != nil {
		t.Fatal(err)
	}
	_ = config.SaveGlobalHostedServer(ts.URL)

	home := t.TempDir()
	existing := "[profiles.keep]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n"
	seedCatalog(t, home, existing)
	t.Setenv("SATELLE_HOME", home)
	path := filepath.Join(home, config.GlobalAgentsName)

	root := &cobra.Command{Use: "satelle"}
	agent := &cobra.Command{Use: "agent"}
	agent.AddCommand(agentProfilesCmd())
	root.AddCommand(agent)
	root.SetArgs([]string{"agent", "profiles", "restore", "--server", ts.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("want login error, got %v", err)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil || string(got) != existing {
		t.Fatalf("existing catalog must be byte-unchanged: %q err=%v", got, rerr)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("401 restore must not create .bak: %v", err)
	}
}

func TestProfilesHelpMentionsForceAndVars(t *testing.T) {
	cmd := agentProfilesCmd()
	help := cmd.Long + "\n"
	for _, c := range cmd.Commands() {
		help += c.Long + "\n" + c.Short + "\n"
	}
	for _, want := range []string{"[vars]", "--force", "satelle login", "personal"} {
		if !strings.Contains(help, want) {
			t.Errorf("profiles help missing %q:\n%s", want, help)
		}
	}
}
