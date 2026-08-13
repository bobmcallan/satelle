package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestScaffoldTomlDocumentsConfigSurface is the sty_8966f18a guard: the init
// scaffold documents every top-level config.Config table/key, stays fully
// commented except the seeded [gate] and [review] tables, and the
// sentinel-wrapped [sync]/[vars] examples uncomment-and-load via
// config.Load. [review] gate_create = true is seeded ON (sty_83782ffb).
func TestScaffoldTomlDocumentsConfigSurface(t *testing.T) {
	if strings.Contains(scaffoldToml, "[hosted]") {
		t.Fatal("scaffold must not present a repo [hosted] table")
	}
	// AC3: every live top-level Config toml key/table appears in the scaffold.
	for _, want := range []string{
		"data_dir", "db", "substrate_roots", "log_level",
		"logs_max_size_kb", "logs_max_files", "stories_keep_closed", "stories_keep_days",
		"[review]", "[gate]", "[vars]", "[sync]",
		// AC1 pointers
		"local|personal|shared", "satelle project bind", "satelle login",
		`all = "personal"`,
		// sty_698e70b6: .local exclusion is documented where the scope ladder is.
		".local",
	} {
		if !strings.Contains(scaffoldToml, want) {
			t.Errorf("scaffoldToml missing config surface token %q", want)
		}
	}

	// AC4: only [gate] + edit_exempt_paths and [review] + gate_create are active.
	for i, line := range strings.Split(scaffoldToml, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		switch s {
		case "[gate]", "edit_exempt_paths = " + defaultEditExemptTOML(),
			"edit_exempt_globs = " + defaultEditExemptGlobsTOML(),
			"[review]", "gate_create = true":
			// expected seeded active lines
		default:
			t.Errorf("scaffold line %d is active (want fully-commented except [gate]/[review]): %q", i+1, s)
		}
	}

	// AC2: uncomment the sentinel example block and Load parses cleanly.
	example := uncommentScaffoldExample(t, scaffoldToml)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, config.ConfigName)
	// Seed the active [gate] table so Load sees a complete zero-config shape,
	// then append the uncommented example block (sync/hosted/vars only).
	body := "[gate]\nedit_exempt_paths = [\".satelle/\", \".gitignore\"]\n\n" + example
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load of uncommented scaffold example: %v\n--- body ---\n%s", err, body)
	}
	if got := cfg.Sync["all"]; got != "personal" {
		t.Errorf("Sync[all] = %q, want personal", got)
	}
	if got := cfg.Sync["documents"]; got != "personal" {
		t.Errorf("Sync[documents] = %q, want personal", got)
	}
	if got := cfg.Sync["project"]; got != "my-project-slug" {
		t.Errorf("Sync[project] = %q, want my-project-slug", got)
	}
	if got := cfg.Sync["server"]; got == "" {
		t.Error("Sync[server] empty after uncommenting example")
	}
	if cfg.Vars["MODEL_BASE_URL"] == "" {
		t.Error("Vars[MODEL_BASE_URL] empty after uncommenting example")
	}
}

// uncommentScaffoldExample extracts lines between the satelle-example markers
// and strips the leading "# " / "#" comment prefix so the block is real TOML.
func uncommentScaffoldExample(t *testing.T, scaffold string) string {
	t.Helper()
	const startMark = "# >>> satelle-example:"
	const endMark = "# <<< satelle-example"
	start := strings.Index(scaffold, startMark)
	end := strings.Index(scaffold, endMark)
	if start < 0 || end < 0 || end <= start {
		t.Fatal("scaffoldToml missing satelle-example sentinel markers")
	}
	// Skip the start marker line itself.
	block := scaffold[start:end]
	lines := strings.Split(block, "\n")
	var out []string
	for i, line := range lines {
		if i == 0 {
			continue // marker
		}
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "# ") {
			out = append(out, strings.TrimPrefix(s, "# "))
			continue
		}
		if strings.HasPrefix(s, "#") {
			out = append(out, strings.TrimPrefix(s, "#"))
			continue
		}
		t.Fatalf("example line not commented: %q", line)
	}
	return strings.Join(out, "\n") + "\n"
}
