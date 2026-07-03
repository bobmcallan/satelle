package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// appAt builds the minimal *app.App requireAgents reads (only DBPath), rooted
// at a temp .satelle dir.
func appAt(t *testing.T) (*app.App, string) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &app.App{DBPath: filepath.Join(dataDir, "satelle.db")}, dataDir
}

// A missing agents.toml in an initialized repo is a BROKEN deployment — the
// bootstrap refuses with the fix (`satelle init`), never silently falling back
// to compiled defaults (sty_d0d6bb67).
func TestRequireAgentsMissingRefuses(t *testing.T) {
	a, _ := appAt(t)
	_, err := requireAgents(a)
	if err == nil {
		t.Fatal("want refusal for a missing agents.toml")
	}
	for _, want := range []string{config.AgentsConfigName, "satelle init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should carry %q: %v", want, err)
		}
	}
}

// A malformed agents.toml refuses with an error naming the file.
func TestRequireAgentsMalformedRefuses(t *testing.T) {
	a, dataDir := appAt(t)
	if err := os.WriteFile(filepath.Join(dataDir, config.AgentsConfigName), []byte("[reviewer\nnot toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := requireAgents(a)
	if err == nil {
		t.Fatal("want refusal for a malformed agents.toml")
	}
	if !strings.Contains(err.Error(), config.AgentsConfigName) {
		t.Errorf("error should name the file: %v", err)
	}
}

// A repo still carrying the retired actors.toml (and no agents.toml) refuses
// with the rename instruction.
func TestRequireAgentsLegacyActorsRefusesWithRename(t *testing.T) {
	a, dataDir := appAt(t)
	if err := os.WriteFile(filepath.Join(dataDir, config.ActorsConfigName), []byte("[reviewer]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := requireAgents(a)
	if err == nil {
		t.Fatal("want refusal when only the retired actors.toml is present")
	}
	if !strings.Contains(err.Error(), config.ActorsConfigName) || !strings.Contains(err.Error(), "rename") {
		t.Errorf("error should name actors.toml and the rename: %v", err)
	}
}

// A well-formed agents.toml loads; the configuration executes as defined.
func TestRequireAgentsWellFormedLoads(t *testing.T) {
	a, dataDir := appAt(t)
	body := "[executor]\nharness = \"in-loop\"\n\n[reviewer]\ntools = \"Read\"\nmodel = \"sonnet\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, config.AgentsConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	agents, err := requireAgents(a)
	if err != nil {
		t.Fatalf("well-formed agents.toml must load: %v", err)
	}
	if rb := agents.ReviewerBinding(); rb.Model != "sonnet" || rb.Tools != "Read" {
		t.Errorf("binding not read from the file: %+v", rb)
	}
}
