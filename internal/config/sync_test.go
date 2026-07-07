package config

import "testing"

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
