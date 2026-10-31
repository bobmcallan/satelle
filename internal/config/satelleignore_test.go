package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnoreMatchTable(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		rel     string
		ignored bool
	}{
		{"no file: ordinary path", "", "skills/a.md", false},
		{"comment and blank lines are skipped", "# secret.md\n\n", "secret.md", false},
		{"basename at any depth", "secret.md\n", "workflows/deep/secret.md", true},
		{"basename at the top", "secret.md\n", "secret.md", true},
		{"star", "*.bak\n", "skills/x.bak", true},
		{"star does not cross /", "skills/*.bak\n", "skills/sub/x.bak", false},
		{"question mark", "a?.md\n", "skills/ab.md", true},
		{"class", "[ab].md\n", "skills/b.md", true},
		{"class miss", "[ab].md\n", "skills/c.md", false},
		{"anchored by a middle slash", "skills/a.md\n", "skills/a.md", true},
		{"anchored does not match deeper", "skills/a.md\n", "x/skills/a.md", false},
		{"anchored by a leading slash", "/a.md\n", "a.md", true},
		{"leading slash does not match deeper", "/a.md\n", "skills/a.md", false},
		{"directory pattern ignores everything beneath", "drafts/\n", "skills/drafts/x.md", true},
		{"directory pattern needs a directory", "drafts/\n", "skills/drafts", false},
		{"double star prefix", "**/x.md\n", "a/b/x.md", true},
		{"double star middle", "a/**/x.md\n", "a/b/c/x.md", true},
		{"double star middle, none between", "a/**/x.md\n", "a/x.md", true},
		{"double star suffix", "scratch/**\n", "scratch/a/b.md", true},
		{"negation lifts an earlier match", "*.md\n!keep.md\n", "keep.md", false},
		{"negation leaves the rest ignored", "*.md\n!keep.md\n", "other.md", true},
		{"last match wins", "!keep.md\n*.md\n", "keep.md", true},
		{"negation cannot lift a path under an ignored directory", "drafts/\n!drafts/x.md\n", "drafts/x.md", true},
		{"escaped leading hash is a literal", "\\#notes.md\n", "#notes.md", true},
		{"windows line endings", "secret.md\r\n", "secret.md", true},
		{"default exclusion survives a ./ segment", "", "config/./.env", true},
		{"default exclusion survives a doubled slash", "", "config//.env", true},
		{"default exclusion survives a .. segment", "", "workflows/../config/.env", true},
		{"default exclusion survives a leading ./", "", "./config/.env", true},
		{"listed path survives a .. segment", "secret.md\n", "skills/../workflows/secret.md", true},
		{"a .. segment does not ignore the sibling", "secret.md\n", "skills/../workflows/other.md", false},
		{"malformed glob never matches", "[a\n", "a", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseIgnore(c.file).Match(c.rel); got != c.ignored {
				t.Errorf("Match(%q) with %q = %v, want %v", c.rel, c.file, got, c.ignored)
			}
		})
	}
}

// config/.env is excluded with no .satelleignore entry, and no "!" line lifts it:
// it is the safety floor the remote's secrets rely on.
func TestIgnoreDefaultEnvExclusion(t *testing.T) {
	var zero Ignore
	if !zero.Match("config/.env") {
		t.Error("config/.env must be ignored with no .satelleignore")
	}
	if zero.Match("config/x.toml") {
		t.Error("config/x.toml must not be ignored by default")
	}
	if zero.Match("skills/config/.env.md") {
		t.Error("only the data dir's config/.env is the default exclusion")
	}
	lifted := ParseIgnore("!config/.env\n!.env\n")
	if !lifted.Match("config/.env") {
		t.Error("a ! line must not lift the config/.env default")
	}
}

func TestLoadIgnore(t *testing.T) {
	dir := t.TempDir()
	ign, err := LoadIgnore(dir)
	if err != nil {
		t.Fatalf("no file must not be an error: %v", err)
	}
	if ign.Match("workflows/secret.md") || !ign.Match("config/.env") {
		t.Error("a missing .satelleignore must give the default patterns alone")
	}
	if err := os.WriteFile(filepath.Join(dir, IgnoreFileName), []byte("workflows/secret.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ign, err = LoadIgnore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ign.Match("workflows/secret.md") || !ign.Match("config/.env") || ign.Match("workflows/other.md") {
		t.Error("LoadIgnore must apply the file's patterns and the default")
	}
}

// An unreadable .satelleignore is an error, never an empty list that lets an
// excluded path through.
func TestLoadIgnoreUnreadableIsAnError(t *testing.T) {
	dir := t.TempDir()
	// A directory in the file's place cannot be read as a pattern list.
	if err := os.Mkdir(filepath.Join(dir, IgnoreFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIgnore(dir); err == nil {
		t.Fatal("expected an error for an unreadable .satelleignore")
	}
}

func TestAssembleWithholdsIgnoredPaths(t *testing.T) {
	in := []ConfigFile{
		{Path: "skills/keep.md", Content: []byte("ok")},
		{Path: "skills/secret.md", Content: []byte("s")},
		{Path: "config/.env", Content: []byte("TOKEN=1")},
	}
	b := assemble(in, ParseIgnore("skills/secret.md\n"))
	if len(b.Files) != 1 || b.Files[0].Path != "skills/keep.md" {
		t.Fatalf("Files = %+v, want only skills/keep.md", b.Files)
	}
	if strings.Join(b.Withheld, ",") != "config/.env,skills/secret.md" {
		t.Errorf("Withheld = %v, want config/.env and skills/secret.md", b.Withheld)
	}
}

// An ignored path belongs to no area, so a hosted copy of it is not a difference.
func TestSyncAreaOfIgnoredPathOwnsNothing(t *testing.T) {
	cfg := Config{}
	if got := SyncAreaOf(cfg, t.TempDir(), "workflows/secret.md", Ignore{}); got != "workflows" {
		t.Fatalf("SyncAreaOf without an ignore = %q, want workflows", got)
	}
	if got := SyncAreaOf(cfg, t.TempDir(), "workflows/secret.md", ParseIgnore("secret.md\n")); got != "" {
		t.Errorf("SyncAreaOf of an ignored path = %q, want none", got)
	}
}

// ConfigFiles never sends a file the .satelleignore lists, and a repo with no
// .satelleignore bundles exactly what it did before.
func TestConfigFilesHonoursIgnore(t *testing.T) {
	repo := t.TempDir()
	data := filepath.Join(repo, ".satelle")
	for _, f := range []string{"skills/keep.md", "skills/secret.md", "skills/sub/secret.md"} {
		p := filepath.Join(data, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\nname: x\n---\nbody "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{}
	paths := func(b Bundle) string {
		var ps []string
		for _, f := range b.Files {
			ps = append(ps, f.Path)
		}
		return strings.Join(ps, ",")
	}
	plain, err := ConfigFiles(cfg, PersonalScope, repo, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(plain); got != "skills/keep.md,skills/secret.md,skills/sub/secret.md" {
		t.Fatalf("without an ignore = %s", got)
	}
	ign, err := LoadIgnore(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ConfigFiles(cfg, PersonalScope, repo, ign); paths(got) != paths(plain) {
		t.Fatalf("no .satelleignore must bundle as before, got %s", paths(got))
	}
	if err := os.WriteFile(filepath.Join(data, IgnoreFileName), []byte("secret.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ign, err = LoadIgnore(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConfigFiles(cfg, PersonalScope, repo, ign)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(b); got != "skills/keep.md" {
		t.Errorf("with secret.md ignored, Files = %s, want only skills/keep.md", got)
	}
	if strings.Join(b.Withheld, ",") != "skills/secret.md,skills/sub/secret.md" {
		t.Errorf("Withheld = %v", b.Withheld)
	}
}
