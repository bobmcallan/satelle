package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitignoreHasSetting (sty_dbc0be85 AC2): a setting is a positive or
// negated rule targeting the path or a path under it.
func TestGitignoreHasSetting(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       bool
	}{
		{"exact", ".satelle", true},
		{"leading slash", "/.satelle", true},
		{"trailing slash", ".satelle/", true},
		{"slash star star", ".satelle/**", true},
		{"slash star", ".satelle/*", true},
		{"negation under it", "!.satelle/x", true},
		{"sub-path only", ".satelle/logs", true},
		{"comment", "# .satelle/", false},
		{"unrelated", "node_modules/", false},
		{"prefix is not a setting", ".satellex/", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitignoreHasSetting(tc.text, ".satelle"); got != tc.want {
				t.Errorf("gitignoreHasSetting(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestGitignoreNeededSatelle (sty_dbc0be85 AC2): the .satelle rule is added
// once and only when the operator has no setting for it.
func TestGitignoreNeededSatelle(t *testing.T) {
	globs := strings.Join(managedEditExemptGlobs, ",")
	localLines := ".satelle/logs,.satelle/satelle.local.toml,.satelle/workflows/agents.local.toml,.satelle/satelle"
	repo := t.TempDir()

	t.Run("no setting", func(t *testing.T) {
		first := gitignoreNeeded("", repo)
		if strings.Join(first, ",") != ".satelle/,"+globs {
			t.Fatalf("first scaffold = %v", first)
		}
		if second := gitignoreNeeded(strings.Join(first, "\n")+"\n", repo); len(second) != 0 {
			t.Errorf("second run must add nothing, got %v", second)
		}
	})
	t.Run("empty file equals missing file", func(t *testing.T) {
		if got := gitignoreNeeded("\n", repo); strings.Join(got, ",") != ".satelle/,"+globs {
			t.Errorf("empty text = %v", got)
		}
	})
	t.Run("existing ignore", func(t *testing.T) {
		if got := gitignoreNeeded("/.satelle\n", repo); strings.Join(got, ",") != globs {
			t.Errorf("an existing ignore must be untouched, got %v", got)
		}
	})
	t.Run("negation adds only local lines", func(t *testing.T) {
		got := gitignoreNeeded("!.satelle/workflows/agents.toml\n", repo)
		if strings.Join(got, ",") != localLines+","+globs {
			t.Errorf("negation = %v", got)
		}
		settled := "!.satelle/workflows/agents.toml\n" + strings.ReplaceAll(localLines, ",", "\n") + "\n" + strings.ReplaceAll(globs, ",", "\n") + "\n"
		if got := gitignoreNeeded(settled, repo); len(got) != 0 {
			t.Errorf("a settled negation file needs nothing, got %v", got)
		}
	})
	t.Run("tracked files", func(t *testing.T) {
		r := gitInitRepo(t, t.TempDir())
		writeFile(t, r, ".satelle/workflows/agents.toml", "[executor]\n")
		gitIn(t, r, "add", "-f", ".satelle")
		gitIn(t, r, "commit", "-qm", "track process")
		got := gitignoreNeeded("", r)
		if strings.Join(got, ",") != localLines+","+globs {
			t.Errorf("tracked .satelle must get no .satelle/ line, got %v", got)
		}
	})
}

// TestGitignoreNeededHarness (sty_dbc0be85 AC3): a harness dir present in the
// repo is ignored by default, once, unless the operator already has a setting
// for it or git tracks files under it.
func TestGitignoreNeededHarness(t *testing.T) {
	settledSatelle := ".satelle/\n" + strings.Join(managedEditExemptGlobs, "\n") + "\n"
	for _, dir := range []string{".claude", ".grok"} {
		mk := func(t *testing.T) string {
			r := gitInitRepo(t, t.TempDir())
			if err := os.MkdirAll(filepath.Join(r, dir), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, r, dir+"/settings.json", "{}\n")
			return r
		}
		t.Run(dir+" no setting", func(t *testing.T) {
			r := mk(t)
			got := gitignoreNeeded(settledSatelle, r)
			if strings.Join(got, ",") != dir+"/" {
				t.Fatalf("got %v, want %s/", got, dir)
			}
			if again := gitignoreNeeded(settledSatelle+dir+"/\n", r); len(again) != 0 {
				t.Errorf("second run must add nothing, got %v", again)
			}
		})
		t.Run(dir+" ignored", func(t *testing.T) {
			if got := gitignoreNeeded(settledSatelle+"/"+dir+"/\n", mk(t)); len(got) != 0 {
				t.Errorf("got %v", got)
			}
		})
		t.Run(dir+" negation", func(t *testing.T) {
			text := settledSatelle + dir + "/*\n!" + dir + "/settings.json\n"
			if got := gitignoreNeeded(text, mk(t)); len(got) != 0 {
				t.Errorf("got %v", got)
			}
		})
		t.Run(dir+" tracked", func(t *testing.T) {
			r := mk(t)
			gitIn(t, r, "add", "-f", dir)
			gitIn(t, r, "commit", "-qm", "commit the harness dir")
			if got := gitignoreNeeded(settledSatelle, r); len(got) != 0 {
				t.Errorf("a committed harness dir is left alone, got %v", got)
			}
		})
	}
	t.Run("absent dir", func(t *testing.T) {
		r := gitInitRepo(t, t.TempDir())
		if got := gitignoreNeeded(settledSatelle, r); len(got) != 0 {
			t.Errorf("no harness dir present, got %v", got)
		}
	})
}

// TestGitignoreHelpDescribesAddOnly (sty_dbc0be85 AC6): init and migrate help
// describe the add-only behaviour, not a rewrite of the block.
func TestGitignoreHelpDescribesAddOnly(t *testing.T) {
	for _, name := range []string{"init", "migrate"} {
		var long string
		for _, c := range NewRootCmd().Commands() {
			if c.Name() == name {
				long = c.Long
			}
		}
		if long == "" {
			t.Fatalf("%s command missing", name)
		}
		for _, banned := range []string{"rewritten between its markers", "rewrite the managed .gitignore block"} {
			if strings.Contains(long, banned) {
				t.Errorf("%s help still claims %q", name, banned)
			}
		}
		if !strings.Contains(long, "missing") {
			t.Errorf("%s help must say only missing entries are added:\n%s", name, long)
		}
	}
}
