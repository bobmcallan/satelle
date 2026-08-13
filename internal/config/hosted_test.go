package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostedServerForPrecedence (sty_34037275): machine [hosted] server only;
// a repo [sync] server is ignored.
func TestHostedServerForPrecedence(t *testing.T) {
	cases := []struct {
		name         string
		global, repo string
		want         string
	}{
		{"global wins over repo", "https://global/", "https://repo", "https://global"},
		{"repo sync server ignored when global empty", "", "https://from-sync/", DefaultHostedServer},
		{"default when neither set", "", "", DefaultHostedServer},
		{"global normalized", "https://g//", "", "https://g"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gc := GlobalConfig{Hosted: GlobalHostedConfig{Server: c.global}}
			repo := Config{Sync: map[string]string{"server": c.repo}}
			if got := HostedServerFor(gc, repo); got != c.want {
				t.Fatalf("HostedServerFor(%q, sync=%q) = %q, want %q", c.global, c.repo, got, c.want)
			}
		})
	}
}

// TestResolveHostedServerReadsGlobal proves the convenience resolver reads the
// on-disk global config and ignores a leftover repo [sync] server.
func TestResolveHostedServerReadsGlobal(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())

	// No global server → default, even if the repo still names one.
	repo := Config{Sync: map[string]string{"server": "https://legacy-repo/"}}
	if got := ResolveHostedServer(repo); got != DefaultHostedServer {
		t.Fatalf("repo [sync] server must be ignored, got %q", got)
	}

	// Record a global server → it now wins over the repo one.
	if err := SaveGlobalHostedServer("https://global"); err != nil {
		t.Fatal(err)
	}
	if got := ResolveHostedServer(repo); got != "https://global" {
		t.Fatalf("global-first = %q, want https://global", got)
	}
}

// TestSaveGlobalHostedServerRoundTripNoTokens round-trips the global writer,
// proves normalization, that other sections survive, and that no token is ever
// written to the global config (migrating the retired writer's no-tokens intent).
func TestSaveGlobalHostedServerRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	// Seed another section so we can prove it survives the hosted write.
	if err := SaveGlobal(GlobalConfig{UI: UIConfig{Theme: "dark"}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobalHostedServer("https://h/"); err != nil {
		t.Fatal(err)
	}
	gc, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if gc.Hosted.ResolveServer() != "https://h" {
		t.Fatalf("server not normalized/persisted: %+v", gc.Hosted)
	}
	if gc.UI.Theme != "dark" {
		t.Fatalf("unrelated [ui] section lost across the hosted write: %+v", gc.UI)
	}
	// The rendered file carries a [hosted] table and never a token.
	body, _ := os.ReadFile(filepath.Join(home, GlobalConfigName))
	if !strings.Contains(string(body), "[hosted]") {
		t.Fatalf("global config missing [hosted] table:\n%s", body)
	}
	if strings.Contains(string(body), "token") {
		t.Fatalf("global config must never contain a token:\n%s", body)
	}
}

func TestSyncServerIsNotResolved(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo := Config{
		Sync:   map[string]string{"server": "https://from-sync/"},
		Hosted: HostedConfig{Server: "https://from-hosted/"},
	}
	if got := HostedServerFor(GlobalConfig{}, repo); got != DefaultHostedServer {
		t.Fatalf("got %q, want default", got)
	}
}

func TestResolveHostedServerDefaultsWithoutGlobal(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	if got := ResolveHostedServer(Config{}); got != DefaultHostedServer {
		t.Fatalf("got %q, want %s", got, DefaultHostedServer)
	}
}

func TestSlugifyAndResolveBoundProject(t *testing.T) {
	if got := Slugify("My Repo"); got != "my-repo" {
		t.Fatalf("Slugify = %q", got)
	}
	if got := ResolveBoundProject(Config{}, "/tmp/My Repo"); got != "my-repo" {
		t.Fatalf("dirname default = %q", got)
	}
	if got := ResolveBoundProject(Config{Hosted: HostedConfig{Project: "legacy"}}, "/tmp/My Repo"); got != "legacy" {
		t.Fatalf("leftover hosted project = %q", got)
	}
	if got := ResolveBoundProject(Config{Sync: map[string]string{"project": "from-sync"}, Hosted: HostedConfig{Project: "legacy"}}, "/tmp/x"); got != "from-sync" {
		t.Fatalf("sync project = %q", got)
	}
	if got := ResolveBoundProject(Config{}, ""); got != "" {
		t.Fatalf("empty root = %q", got)
	}
}

func TestReservedSyncKeysAreNotAreas(t *testing.T) {
	for _, k := range []string{"server", "project", "workspace", "all"} {
		for _, a := range SyncAreas {
			if a == k {
				t.Fatalf("%q must not be a SyncArea", k)
			}
		}
	}
}

func TestSaveGlobalHostedServerRejectsEmpty(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	if err := SaveGlobalHostedServer("   "); err == nil {
		t.Fatal("expected an error for an empty server URL")
	}
}
