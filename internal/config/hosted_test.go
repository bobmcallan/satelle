package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostedServerForPrecedence pins the resolution rule (sty_53ccf845): the
// global hosted server wins; the repo's committed [hosted] server is only the
// read-only fallback; both are normalized; neither set → "".
func TestHostedServerForPrecedence(t *testing.T) {
	cases := []struct {
		name         string
		global, repo string
		want         string
	}{
		{"global wins over repo", "https://global/", "https://repo", "https://global"},
		{"repo fallback when global empty", "", "https://repo/", "https://repo"},
		{"empty when neither set", "", "", ""},
		{"global normalized", "https://g//", "", "https://g"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gc := GlobalConfig{Hosted: GlobalHostedConfig{Server: c.global}}
			repo := Config{Hosted: HostedConfig{Server: c.repo}}
			if got := HostedServerFor(gc, repo); got != c.want {
				t.Fatalf("HostedServerFor(%q,%q) = %q, want %q", c.global, c.repo, got, c.want)
			}
		})
	}
}

// TestResolveHostedServerReadsGlobal proves the convenience resolver reads the
// on-disk global config and applies the precedence rule (global-first), with the
// repo server as the backward-compat fallback for a repo bound before the global
// model.
func TestResolveHostedServerReadsGlobal(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())

	// No global server yet → the repo's legacy [hosted] server still resolves.
	repo := Config{Hosted: HostedConfig{Server: "https://legacy-repo/"}}
	if got := ResolveHostedServer(repo); got != "https://legacy-repo" {
		t.Fatalf("repo fallback = %q, want https://legacy-repo", got)
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

func TestSaveGlobalHostedServerRejectsEmpty(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	if err := SaveGlobalHostedServer("   "); err == nil {
		t.Fatal("expected an error for an empty server URL")
	}
}
