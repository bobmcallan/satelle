package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/logsread"
)

func TestCmdLogsPathAndTail(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	rt := filepath.Join(os.Getenv("SATELLE_HOME"), filepath.Base(repo)) // may not match RepoKey
	// Drive Select via planted files and the cobra command using SATELLE_CONFIG.
	logs := t.TempDir()
	disp := filepath.Join(logs, "dispatch")
	if err := os.MkdirAll(disp, 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := 1; i <= 60; i++ {
		body.WriteString("line-" + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(filepath.Join(disp, logsread.FormatDispatchName("reviewer", "sty_x", 50)), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disp, logsread.FormatDispatchName("reviewer", "sty_x", 10)), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disp, logsread.FormatDispatchName("planner", "sty_x", 90)), []byte("decoy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, ok, err := logsread.Select(logs, "sty_x", "reviewer")
	if err != nil || !ok {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(f.Path)
	got := logsread.LastNLines(string(raw), 50)
	if len(got) != 50 || got[0] != "line-11" || got[len(got)-1] != "line-60" {
		t.Fatalf("tail %v", got)
	}
	_ = rt
}

func TestSatelleLogsStoryRoleTail(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	cfg, cfgPath, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	logs := cfg.ResolveLogsDir(config.RepoRootFromConfigPath(cfgPath))
	disp := filepath.Join(logs, "dispatch")
	if err := os.MkdirAll(disp, 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for i := 1; i <= 60; i++ {
		body.WriteString("line-" + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(filepath.Join(disp, logsread.FormatDispatchName("reviewer", "sty_x", 50)), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disp, logsread.FormatDispatchName("reviewer", "sty_x", 10)), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disp, logsread.FormatDispatchName("planner", "sty_x", 90)), []byte("decoy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRootIn(t, "", "logs", "--story", "sty_x", "--role", "reviewer", "--tail", "50")
	if err != nil {
		t.Fatalf("logs: %v\n%s", err, out)
	}
	if strings.Contains(out, "decoy") || strings.Contains(out, "old") {
		t.Fatalf("wrong file:\n%s", out)
	}
	if !strings.Contains(out, "line-11") || !strings.Contains(out, "line-60") {
		t.Fatalf("missing tail lines:\n%s", out)
	}
	if strings.Contains(out, "line-10\n") && !strings.Contains(out, "line-11") {
		t.Fatalf("included line-10:\n%s", out)
	}
}

func TestSatelleLogsPath(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	out, err := runRootIn(t, "", "logs", "--path")
	if err != nil {
		t.Fatalf("logs --path: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty path")
	}
	if strings.Contains(out, "\n#") {
		t.Fatalf("path must be the only output: %q", out)
	}
}
