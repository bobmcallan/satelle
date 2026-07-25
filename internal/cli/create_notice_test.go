package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// preSeedScaffold is the pre-sty_83782ffb shape: [review] fully commented out so
// GateCreate falls back to the zero value and the key is absent for HasKey.
const preSeedScaffold = `# satelle.toml — pre-seed shape
# [review]
# gate_create = true

[gate]
edit_exempt_paths = [".satelle/", ".gitignore"]
`

func writeDataDir(t *testing.T, committed, local string) string {
	t.Helper()
	dataDir := t.TempDir()
	if committed != "" {
		if err := os.WriteFile(filepath.Join(dataDir, config.ConfigName), []byte(committed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(dataDir, config.LocalConfigName), []byte(local), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

func TestCreateGateNoticeUnit(t *testing.T) {
	cases := []struct {
		name      string
		committed string
		local     string
		// when load is true, gateOn is taken from config.Load rather than forced
		load   bool
		gateOn bool
		want   bool // notice non-empty?
	}{
		{
			name:      "pre-seed scaffold",
			committed: preSeedScaffold,
			load:      true,
			want:      true,
		},
		{
			name:      "gated",
			committed: "[review]\ngate_create = true\n",
			load:      true,
			want:      false,
		},
		{
			name:      "explicit opt-out",
			committed: "[review]\ngate_create = false\n",
			load:      true,
			want:      false,
		},
		{
			name: "no satelle.toml at all",
			// gateOn forced false; nothing on disk
			gateOn: false,
			want:   true,
		},
		{
			name:      "overlay defines key",
			committed: "# silent committed\n",
			local:     "[review]\ngate_create = false\n",
			load:      true,
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := writeDataDir(t, tc.committed, tc.local)
			gateOn := tc.gateOn
			if tc.load {
				// Load needs a path to the committed file (or the data dir parent layout).
				cfgPath := filepath.Join(dataDir, config.ConfigName)
				if tc.committed == "" {
					// Load with empty path falls back; force gateOn from the case.
				} else {
					cfg, _, err := config.Load(cfgPath)
					if err != nil && err != config.ErrNotFound {
						t.Fatal(err)
					}
					gateOn = cfg.Review.GateCreate
				}
			}
			notice := createGateNotice(gateOn, dataDir)
			if tc.want {
				if notice == "" {
					t.Fatalf("want notice, got empty")
				}
				if !strings.Contains(notice, "[review]") || !strings.Contains(notice, "gate_create = true") {
					t.Errorf("notice must name the one-line fix, got:\n%s", notice)
				}
				if !strings.Contains(notice, "WITHOUT create review") {
					t.Errorf("notice must say create ran without review:\n%s", notice)
				}
			} else if notice != "" {
				t.Fatalf("want silent, got:\n%s", notice)
			}
		})
	}
}

func TestConfigKeyDefinedOverlay(t *testing.T) {
	dataDir := writeDataDir(t, "# no review table\n", "[review]\ngate_create = true\n")
	if !configKeyDefined(dataDir, "review", "gate_create") {
		t.Fatal("overlay-only definition must count as defined")
	}
	empty := writeDataDir(t, preSeedScaffold, "")
	if configKeyDefined(empty, "review", "gate_create") {
		t.Fatal("commented pre-seed must not count as defined")
	}
}

func TestScaffoldDefaultOwnsGateCreateBlock(t *testing.T) {
	d, ok := scaffoldDefault("review", "gate_create")
	if !ok {
		t.Fatal("scaffoldConfigDefaults must list [review] gate_create")
	}
	if !strings.Contains(d.Block, "gate_create = true") {
		t.Fatalf("Block must carry the enable line, got %q", d.Block)
	}
	// Notice must reuse the table Block, not invent a second copy.
	notice := createGateNotice(false, writeDataDir(t, preSeedScaffold, ""))
	for _, line := range strings.Split(d.Block, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(notice, line) {
			t.Errorf("notice must contain table Block line %q; notice:\n%s", line, notice)
		}
	}
}

// TestCreateGateNoticeOnCreate proves the notice fires on the story-create path
// (AC2) and that the operator's toml is not rewritten (AC3).
func TestCreateGateNoticeOnCreate(t *testing.T) {
	// --- case 6: pre-seed scaffold → notice on create; toml bytes unchanged ---
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", config.ConfigName)
	if err := os.WriteFile(cfgPath, []byte(preSeedScaffold), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "story", "create",
		"--title", "Ungated create probe",
		"--body", "Prove the create-path notice fires for a pre-seed scaffold.",
		"--acceptance", "1. notice appears on stderr",
		"--category", "feature",
	)
	if err != nil {
		t.Fatalf("story create (pre-seed): %v\n%s", err, out)
	}
	if !strings.Contains(out, "WITHOUT create review") {
		t.Errorf("create on pre-seed scaffold must surface the notice:\n%s", out)
	}
	if !strings.Contains(out, "gate_create = true") {
		t.Errorf("notice must name the one-line fix:\n%s", out)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("create must not rewrite satelle.toml\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// --- case 7: gate_create = true → create quiet ---
	//
	// openApp wires verb.SetCreateReviewer(rev) when GateCreate is true. The
	// embedded create_review skill is LLM-backed; override it on disk with a
	// functional-check skill that always accepts so go test stays hermetic
	// (no agent spawn). Clear the package-global afterward so later creates in
	// this package are not gated by a leftover Engine.
	repo2 := tempRepo(t)
	cfgPath2 := filepath.Join(repo2, ".satelle", config.ConfigName)
	gated := "[review]\ngate_create = true\n\n[gate]\nedit_exempt_paths = [\".satelle/\", \".gitignore\"]\n"
	if err := os.WriteFile(cfgPath2, []byte(gated), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(repo2, ".satelle", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Disk wins over the embedded create-review skill (virtual sparse defaults).
	// Functional-check form (check fence) so ReviewCreate never spawns an agent.
	hermeticCreateReview := "---\n" +
		"name: satelle-story-create-review\n" +
		"scope: project\n" +
		"type: skill\n" +
		"tags: [type:skill, type:reviewer]\n" +
		"description: Hermetic always-accept create review for unit tests (functional check, no LLM).\n" +
		"---\n\n" +
		"# Hermetic create review\n\n" +
		"```check\nexit 0\n```\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "satelle-story-create-review.md"), []byte(hermeticCreateReview), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "reindex"); err != nil {
		t.Fatalf("reindex hermetic create-review skill: %v\n%s", err, out)
	}
	t.Cleanup(func() { verb.SetCreateReviewer(nil) })

	out2, err := runRoot(t, "story", "create",
		"--title", "Gated create probe full",
		"--body", "A structurally valid draft for the gated quiet case.",
		"--acceptance", "1. create succeeds without the ungated notice",
		"--category", "feature",
	)
	if err != nil {
		t.Fatalf("story create (gated): %v\n%s", err, out2)
	}
	if strings.Contains(out2, "WITHOUT create review") {
		t.Errorf("gated create must stay silent:\n%s", out2)
	}
}
