package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSatelleToml(t *testing.T, dir, name, body string) string {
	t.Helper()
	sat := filepath.Join(dir, DefaultDataDir)
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sat, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCommandRulesWellFormed(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, `
[[gate.command_rule]]
pattern = "orbctl apply"
action = "deny"
allow = ["in_progress"]
message = "mutating"

[[gate.command_rule]]
pattern = "orbctl scale *"
action = "warn"
message = "scale"
`)
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Gate.CommandRules
	if len(r) != 2 || r[0].Action != CommandRuleDeny || r[0].Allow[0] != "in_progress" || r[1].Action != CommandRuleWarn || len(r[1].Allow) != 0 {
		t.Fatalf("rules = %+v", r)
	}
}

func TestLoadCommandRulesNoneByDefault(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, "[gate]\n")
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Gate.CommandRules) != 0 {
		t.Fatalf("rules = %+v", cfg.Gate.CommandRules)
	}
}

func TestLoadCommandRulesMalformed(t *testing.T) {
	const secret = "SECRETVALUE"
	msg := "message = \"" + secret + "\""
	cases := []struct {
		name, body, field string
		index             int
	}{
		{"empty pattern", "pattern = \"  \"\naction = \"deny\"\n" + msg, "pattern", 0},
		{"shell syntax", "pattern = \"a; b\"\naction = \"deny\"\n" + msg, "pattern", 0},
		{"quoted token", "pattern = \"a 'b'\"\naction = \"deny\"\n" + msg, "pattern", 0},
		{"unknown action", "pattern = \"a\"\naction = \"block\"\n" + msg, "action", 0},
		{"missing action", "pattern = \"a\"\n" + msg, "action", 0},
		{"empty message", "pattern = \"a\"\naction = \"deny\"\nmessage = \" \"", "message", 0},
		{"empty allow entry", "pattern = \"a\"\naction = \"deny\"\nallow = [\"x\", \"\"]\n" + msg, "allow", 0},
		{"unknown key", "pattern = \"a\"\naction = \"deny\"\nalow = [\"x\"]\n" + msg, "alow", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := writeSatelleToml(t, root, ConfigName, "[[gate.command_rule]]\n"+tc.body+"\n")
			_, _, err := Load(p)
			var cre *CommandRuleError
			if !errors.As(err, &cre) {
				t.Fatalf("err = %v, want *CommandRuleError", err)
			}
			if cre.Field != tc.field || cre.Index != tc.index || cre.Path != p {
				t.Fatalf("got %+v, want field %q index %d path %q", cre, tc.field, tc.index, p)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error echoes a value: %v", err)
			}
		})
	}
}

func TestLoadCommandRulesIndexNamesTheBadRule(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, `
[[gate.command_rule]]
pattern = "a"
action = "deny"
message = "ok"

[[gate.command_rule]]
pattern = "b"
action = "block"
message = "ok"
`)
	_, _, err := Load(p)
	var cre *CommandRuleError
	if !errors.As(err, &cre) || cre.Index != 1 || cre.Field != "action" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "rule 1 field action") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestLoadOverlayOnlyMalformedRule(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, "[gate]\n")
	local := writeSatelleToml(t, root, LocalConfigName, `
[[gate.command_rule]]
pattern = "a"
action = "block"
message = "m"
`)
	_, path, err := Load(p)
	var cre *CommandRuleError
	if !errors.As(err, &cre) {
		t.Fatalf("err = %v, want *CommandRuleError", err)
	}
	if cre.Path != local || path != local {
		t.Fatalf("path = %q (returned %q), want the overlay %q", cre.Path, path, local)
	}
}

func TestLoadParseErrorIsTyped(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, "[gate\n")
	_, path, err := Load(p)
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Path != p || path != p {
		t.Fatalf("err = %v path = %q", err, path)
	}
	if !strings.HasPrefix(err.Error(), "config: parse "+p+": ") {
		t.Fatalf("message changed: %q", err.Error())
	}
}
