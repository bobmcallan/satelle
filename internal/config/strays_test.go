package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestMachineScopeStraysTable(t *testing.T) {
	home := testutil.IsolateHome(t)
	if err := SaveGlobal(GlobalConfig{Service: ServiceConfig{Port: 8787, Addr: "0.0.0.0"}}); err != nil {
		t.Fatal(err)
	}
	_ = home

	cases := []struct {
		name    string
		file    string
		body    string
		wantKey string
		wantVal string
	}{
		{"web_port committed", ConfigName, "web_port = 9000\nlog_level = \"info\"\n", "web_port", "9000"},
		{"server endpoint local", LocalConfigName, "[server]\nendpoint = \"http://127.0.0.1:9\"\n", "[server] endpoint", "http://127.0.0.1:9"},
		{"service repo", ConfigName, "[service]\nrepo = \"/old/path\"\n", "[service] repo", "/old/path"},
		{"sync server leftover", ConfigName, "[sync]\nserver = \"https://other.dev\"\nproject = \"satelle\"\n", "[sync] server", "https://other.dev"},
		{"hosted server leftover", ConfigName, "[hosted]\nserver = \"https://legacy.example\"\nproject = \"satelle\"\n", "[hosted] server", "https://legacy.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dataDir, tc.file), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			got := MachineScopeStrays(dataDir)
			if len(got) != 1 {
				t.Fatalf("strays = %#v, want 1", got)
			}
			if got[0].Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got[0].Key, tc.wantKey)
			}
			if got[0].RepoValue != tc.wantVal {
				t.Errorf("RepoValue = %q, want %q", got[0].RepoValue, tc.wantVal)
			}
			if got[0].File != tc.file {
				t.Errorf("File = %q, want %q", got[0].File, tc.file)
			}
			w := got[0].Warning()
			for _, need := range []string{tc.wantKey, got[0].EffectiveValue, "satelle migrate --yes"} {
				if !strings.Contains(w, need) {
					t.Errorf("Warning missing %q:\n%s", need, w)
				}
			}
		})
	}
}

func TestMachineScopeStraysCleanRepoZero(t *testing.T) {
	testutil.IsolateHome(t)
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, ConfigName), []byte("log_level = \"info\"\n\n[review]\ngate_create = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MachineScopeStrays(dataDir); len(got) != 0 {
		t.Fatalf("clean repo strays = %#v", got)
	}
}

func TestMachineScopeStraysIgnoresSyncProjectAndStaleAfter(t *testing.T) {
	testutil.IsolateHome(t)
	dataDir := t.TempDir()
	body := "[sync]\nproject = \"satelle\"\nstale_after = \"24h\"\nall = \"personal\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, ConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MachineScopeStrays(dataDir); len(got) != 0 {
		t.Fatalf("per-repo [sync] keys must not be strays: %#v", got)
	}
}

func TestMachineScopeStraysIgnoresCommentedSyncServer(t *testing.T) {
	testutil.IsolateHome(t)
	dataDir := t.TempDir()
	body := "[sync]\n# server = \"https://other.dev\"\nproject = \"satelle\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, ConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MachineScopeStrays(dataDir); len(got) != 0 {
		t.Fatalf("commented [sync] server must not be a stray: %#v", got)
	}
}

func TestMachineScopeStraysIgnoresComments(t *testing.T) {
	testutil.IsolateHome(t)
	dataDir := t.TempDir()
	body := "# web_port = 9000\nlog_level = \"info\"\n# [server]\n# endpoint = \"http://x\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, ConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MachineScopeStrays(dataDir); len(got) != 0 {
		t.Fatalf("commented keys must not be strays: %#v", got)
	}
}
