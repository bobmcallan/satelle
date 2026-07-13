package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertKeyReplacesInRootPreservingComments(t *testing.T) {
	in := "# a comment\nweb_port = 8787\nlog_level = \"info\"\n\n[review]\ngate_create = true\n"
	out := UpsertKey(in, "", "web_port", "9000")
	if !strings.Contains(out, "web_port = 9000") || strings.Contains(out, "8787") {
		t.Fatalf("web_port not replaced:\n%s", out)
	}
	if !strings.Contains(out, "# a comment") || !strings.Contains(out, `log_level = "info"`) {
		t.Fatalf("neighbours/comment lost:\n%s", out)
	}
	if !strings.Contains(out, "[review]") || !strings.Contains(out, "gate_create = true") {
		t.Fatalf("following table clobbered:\n%s", out)
	}
}

func TestUpsertKeyAddsKeyToExistingSection(t *testing.T) {
	in := "[hosted]\nserver = \"https://h\"\n\n[review]\ngate_create = true\n"
	out := UpsertKey(in, "hosted", "project", "\"proj\"")
	if !strings.Contains(out, `project = "proj"`) {
		t.Fatalf("project not added to [hosted]:\n%s", out)
	}
	// It must land inside [hosted], before [review].
	if strings.Index(out, "project =") > strings.Index(out, "[review]") {
		t.Fatalf("project added outside [hosted]:\n%s", out)
	}
}

func TestUpsertKeyAddsMissingSection(t *testing.T) {
	in := "web_port = 8787\n"
	out := UpsertKey(in, "hosted", "server", "\"https://h\"")
	if !strings.Contains(out, "[hosted]") || !strings.Contains(out, `server = "https://h"`) {
		t.Fatalf("missing section not appended:\n%s", out)
	}
	if !strings.Contains(out, "web_port = 8787") {
		t.Fatalf("root key lost:\n%s", out)
	}
}

func TestUpsertKeyAddsRootKeyBeforeFirstTable(t *testing.T) {
	in := "# header\n[review]\ngate_create = true\n"
	out := UpsertKey(in, "", "log_level", "\"debug\"")
	if strings.Index(out, "log_level") > strings.Index(out, "[review]") {
		t.Fatalf("root key must land before the first table:\n%s", out)
	}
}

func TestSaveConfigValuesRoundTripPreservesUnmodeled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	original := "# keep me\nweb_port = 8787\n# custom_unmodeled below\nfuture_key = \"x\"\n\n[review]\ngate_create = true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	err := SaveConfigValues(path, []KeyEdit{
		{Section: "", Key: "log_level", Value: "\"warn\""},
		{Section: "", Key: "web_port", Value: "9001"},
		{Section: "review", Key: "gate_create", Value: "false"},
		{Section: "gate", Key: "edit_exempt_paths", Value: `[".claude/", "docs/"]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "# keep me") || !strings.Contains(s, `future_key = "x"`) {
		t.Fatalf("unmodeled content lost:\n%s", s)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebPort != 9001 || cfg.LogLevel != "warn" {
		t.Fatalf("scalar edits not applied: %+v", cfg)
	}
	if cfg.Review.GateCreate {
		t.Fatal("gate_create should be false")
	}
	if len(cfg.Gate.EditExemptPaths) != 2 || cfg.Gate.EditExemptPaths[0] != ".claude/" {
		t.Fatalf("list edit not applied: %+v", cfg.Gate.EditExemptPaths)
	}
}

func TestHasKeyDetectsPresentAndAbsent(t *testing.T) {
	in := `[gate]
allow_parallel = false
edit_exempt_paths = [".satelle/"]

[review]
gate_create = true
`
	if !HasKey(in, "gate", "edit_exempt_paths") {
		t.Error("present key should be found")
	}
	if HasKey(in, "gate", "missing_key") {
		t.Error("absent key must not be found")
	}
	if HasKey(in, "hosted", "server") {
		t.Error("absent section must not report key")
	}
	if !HasKey(in, "review", "gate_create") {
		t.Error("review.gate_create present")
	}
}
