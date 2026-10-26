package config

import (
	"os"
	"path/filepath"
	"testing"
)

// [sync] hold_unpushed: missing means true, a boolean sets it, anything else is
// a config error (sty_7361569a).
func TestHoldUnpushed(t *testing.T) {
	cases := []struct {
		name    string
		sync    SyncTable
		want    bool
		wantErr bool
	}{
		{"missing", nil, true, false},
		{"blank", SyncTable{"hold_unpushed": " "}, true, false},
		{"true", SyncTable{"hold_unpushed": "true"}, true, false},
		{"false", SyncTable{"hold_unpushed": "false"}, false, false},
		{"bad", SyncTable{"hold_unpushed": "maybe"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HoldUnpushed(Config{Sync: tc.sync})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("HoldUnpushed = %v, want %v", got, tc.want)
			}
		})
	}
}

// A satelle.toml still carrying the retired [sync] hold_stale_after key (hosted
// holds are gone, sty_40853d02) loads without error, the key disturbs no area
// and is never minted as one.
func TestRetiredHoldKeyLoadsSilently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "satelle.toml")
	if err := os.WriteFile(path, []byte("[sync]\nstale_after = \"24h\"\nhold_stale_after = \"2h\"\nstories = \"personal\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("a config carrying the retired key must load: %v", err)
	}
	if sc, err := ScopeFor(cfg, "stories"); err != nil || sc == LocalScope {
		t.Errorf("stories scope = %v, %v; the retired key must not disturb the areas", sc, err)
	}
	for _, a := range SyncAreas {
		if a == "hold_stale_after" {
			t.Error("hold_stale_after minted as a sync area")
		}
	}
}

// A bare TOML boolean loads, the overlay wins per key, and hold_unpushed is a
// reserved key rather than a sync area.
func TestSyncTableAcceptsBareBoolean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	if err := os.WriteFile(path, []byte("[sync]\nstories = \"personal\"\nhold_unpushed = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sync["stories"] != "personal" {
		t.Errorf("stories = %q, want personal", cfg.Sync["stories"])
	}
	if got, err := HoldUnpushed(cfg); err != nil || got {
		t.Errorf("HoldUnpushed = %v, %v; want false, nil", got, err)
	}
	if !reservedSyncKeys[syncHoldUnpushedKey] {
		t.Error("hold_unpushed must be a reserved [sync] key")
	}
	for _, a := range SyncAreas {
		if a == syncHoldUnpushedKey {
			t.Errorf("hold_unpushed minted as a sync area")
		}
	}

	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("[sync]\nhold_unpushed = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(bad); err == nil {
		t.Error("an integer [sync] value must be a config error")
	}
}

func TestParseScope(t *testing.T) {
	cases := map[string]Scope{"local": LocalScope, "personal": PersonalScope, "shared": SharedScope}
	for raw, want := range cases {
		got, err := ParseScope(raw)
		if err != nil {
			t.Fatalf("ParseScope(%q) error: %v", raw, err)
		}
		if got != want {
			t.Errorf("ParseScope(%q) = %v, want %v", raw, got, want)
		}
		if got.String() != raw {
			t.Errorf("Scope(%v).String() = %q, want %q", got, got.String(), raw)
		}
	}
	if _, err := ParseScope("typo"); err == nil {
		t.Error("ParseScope(\"typo\") did not error")
	}
}

func TestScopeFor(t *testing.T) {
	cfg := Config{Sync: map[string]string{"skills": "personal", "documents": "shared"}}

	if got, err := ScopeFor(cfg, "skills"); err != nil || got != PersonalScope {
		t.Errorf("ScopeFor(skills) = %v, %v; want PersonalScope, nil", got, err)
	}
	if got, err := ScopeFor(cfg, "documents"); err != nil || got != SharedScope {
		t.Errorf("ScopeFor(documents) = %v, %v; want SharedScope, nil", got, err)
	}
	// Unset area → local (the default).
	if got, err := ScopeFor(cfg, "workflows"); err != nil || got != LocalScope {
		t.Errorf("ScopeFor(unset) = %v, %v; want LocalScope, nil", got, err)
	}
	// Explicitly set but invalid → error, never a silent local.
	bad := Config{Sync: map[string]string{"tasks": "typo"}}
	if _, err := ScopeFor(bad, "tasks"); err == nil {
		t.Error("ScopeFor(invalid scope) did not error")
	}
}

func TestScopeForAllFallthrough(t *testing.T) {
	// Blanket all = personal: every SyncAreas name resolves to personal, but
	// `all` itself is not a walk area.
	blanket := Config{Sync: map[string]string{"all": "personal"}}
	for _, area := range SyncAreas {
		if got, err := ScopeFor(blanket, area); err != nil || got != PersonalScope {
			t.Errorf("ScopeFor(%q) under all=personal = %v, %v; want PersonalScope, nil", area, got, err)
		}
	}

	// A per-area key overrides all; a blank per-area value falls through to all.
	override := Config{Sync: map[string]string{"all": "personal", "skills": "shared", "documents": ""}}
	if got, err := ScopeFor(override, "skills"); err != nil || got != SharedScope {
		t.Errorf("ScopeFor(skills) with override = %v, %v; want SharedScope, nil", got, err)
	}
	if got, err := ScopeFor(override, "documents"); err != nil || got != PersonalScope {
		t.Errorf("ScopeFor(blank documents) = %v, %v; want PersonalScope (falls through to all), nil", got, err)
	}
	if got, err := ScopeFor(override, "tasks"); err != nil || got != PersonalScope {
		t.Errorf("ScopeFor(unset tasks) = %v, %v; want PersonalScope (falls through to all), nil", got, err)
	}

	// Unset all still defaults areas to local.
	if got, err := ScopeFor(Config{Sync: map[string]string{"all": ""}}, "skills"); err != nil || got != LocalScope {
		t.Errorf("ScopeFor(skills) under blank all = %v, %v; want LocalScope, nil", got, err)
	}

	// Invalid all errors ONLY for areas that fall through to it; a valid
	// per-area override beside a bad all resolves without error.
	badAll := Config{Sync: map[string]string{"all": "typo", "skills": "personal"}}
	if _, err := ScopeFor(badAll, "tasks"); err == nil {
		t.Error("ScopeFor(tasks) under all=typo did not error on fallthrough")
	}
	if got, err := ScopeFor(badAll, "skills"); err != nil || got != PersonalScope {
		t.Errorf("ScopeFor(skills override) beside bad all = %v, %v; want PersonalScope, nil (override wins)", got, err)
	}
}

func TestFileShared(t *testing.T) {
	sharedFM := "---\ntype: skill\nshared: true\n---\nbody"
	unsetFM := "---\ntype: skill\n---\nbody"
	falseFM := "---\ntype: skill\nshared: false\n---\nbody"
	garbageFM := "---\ntype: skill\nshared: yes\n---\nbody"

	if !FileShared(PersonalScope, sharedFM) {
		t.Error("FileShared(personal, shared:true) = false, want true")
	}
	if FileShared(PersonalScope, unsetFM) {
		t.Error("FileShared(personal, no shared key) = true, want false")
	}
	if FileShared(PersonalScope, falseFM) {
		t.Error("FileShared(personal, shared:false) = true, want false")
	}
	if FileShared(PersonalScope, garbageFM) {
		t.Error("FileShared(personal, shared:yes) = true, want false (fail closed)")
	}
	// The flag is only meaningful inside a personal area — local/shared ignore it
	// entirely, even when the frontmatter itself says shared: true.
	if FileShared(LocalScope, sharedFM) {
		t.Error("FileShared(local, shared:true) = true, want false — flag ignored outside personal")
	}
	if FileShared(SharedScope, sharedFM) {
		t.Error("FileShared(shared, shared:true) = true, want false — flag ignored outside personal")
	}
}

func TestSyncAreasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range SyncAreas {
		if seen[a] {
			t.Errorf("SyncAreas contains duplicate %q", a)
		}
		seen[a] = true
	}
	for _, kind := range AuthoredKinds {
		if !seen[kind] {
			t.Errorf("SyncAreas missing AuthoredKind %q", kind)
		}
	}
}

// AreaOfRepoPath maps the repo-relative paths story-diff reports to the sync area
// that would push them — through ConfigAreaLocation, so layout is honoured, and
// through walkAreaFiles' skip rules, so a path no push carries maps to no area.
func TestAreaOfRepoPath(t *testing.T) {
	root := t.TempDir()
	type tc struct{ path, want string }
	run := func(t *testing.T, cfg Config, cases []tc) {
		t.Helper()
		for _, c := range cases {
			if got := AreaOfRepoPath(cfg, root, c.path); got != c.want {
				t.Errorf("AreaOfRepoPath(%q) = %q, want %q", c.path, got, c.want)
			}
		}
	}

	t.Run("default layout", func(t *testing.T) {
		run(t, Config{}, []tc{
			{".satelle/skills/x.md", "skills"},
			{".satelle/skills/nested/y.md", "skills"},
			{".satelle/principles/p.md", "principles"},
			{".satelle/workflows/w.toml", "workflows"},
			{".satelle/documents/d.md", "documents"},
			{".satelle/tasks/t.md", "tasks"},
			{".satelle/satelle.toml", "settings"},
			{".satelle/constitution.md", "constitution"},
			{".satelle/workflows/agents.toml", "agents"},
			{".satelle/workflows/agents.workspace.toml", ""},
			{".satelle/skills/index.md", ""},
			{".satelle/skills/log.md", ""},
			{".satelle/documents/README.md", ""},
			{".satelle/skills", ""},
			{".satelle/runtime/state.json", ""},
			{"internal/x.go", ""},
			{"skills/x.md", ""},
			{"../outside/skills/x.md", ""},
			{filepath.Join(root, ".satelle", "skills", "abs.md"), "skills"},
		})
	})

	t.Run("non-default data dir", func(t *testing.T) {
		run(t, Config{DataDir: "meta"}, []tc{
			{"meta/skills/x.md", "skills"},
			{"meta/satelle.toml", "settings"},
			{"meta/constitution.md", "constitution"},
			{"meta/workflows/agents.toml", "agents"},
			{"meta/workflows/agents.workspace.toml", ""},
			{".satelle/skills/x.md", ""},
		})
	})

	t.Run("relocated substrate root", func(t *testing.T) {
		run(t, Config{SubstrateRoots: map[string]string{"skills": "process"}}, []tc{
			{"process/skills/x.md", "skills"},
			{".satelle/skills/x.md", ""},
			{".satelle/principles/p.md", "principles"},
		})
	})
}
