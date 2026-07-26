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

func TestListValueContainsAndListStringValues(t *testing.T) {
	in := `[gate]
edit_exempt_paths = [".satelle/", ".claude/"]

[review]
gate_create = true
`
	got := ListStringValues(in, "gate", "edit_exempt_paths")
	if len(got) != 2 || got[0] != ".satelle/" || got[1] != ".claude/" {
		t.Fatalf("ListStringValues = %v", got)
	}
	if !ListValueContains(in, "gate", "edit_exempt_paths", ".satelle/") {
		t.Error("should contain .satelle/")
	}
	if ListValueContains(in, "gate", "edit_exempt_paths", ".gitignore") {
		t.Error("must not claim missing value is present")
	}
	if ListValueContains(in, "gate", "missing", ".satelle/") {
		t.Error("absent key must be false")
	}
	// Empty list: present key, zero values, non-nil slice.
	empty := "[gate]\nedit_exempt_paths = []\n"
	if items := ListStringValues(empty, "gate", "edit_exempt_paths"); items == nil || len(items) != 0 {
		t.Fatalf("empty list: got %#v", items)
	}
	if ListStringValues(in, "hosted", "project") != nil {
		t.Error("absent section must return nil")
	}
}

func TestRemoveKey(t *testing.T) {
	in := "[sync]\nall = \"personal\"\n\n[hosted]\nproject = \"alpha\"\nserver = \"https://x\"\n"
	got := RemoveKey(in, "hosted", "project")
	if strings.Contains(got, `project = "alpha"`) {
		t.Errorf("project not removed: %s", got)
	}
	if !strings.Contains(got, `all = "personal"`) {
		t.Errorf("sync lost: %s", got)
	}
	if !strings.Contains(got, "server") {
		t.Errorf("server lost: %s", got)
	}
	// no-op when missing
	if RemoveKey(in, "hosted", "missing") != in {
		t.Error("missing key should be no-op")
	}
}
