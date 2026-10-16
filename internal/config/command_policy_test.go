package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// AC2: a malformed subscription is refused at load, naming the file, index and
// field and never a value.
func TestLoadCommandPolicyMalformed(t *testing.T) {
	const src = "url = \"https://example.com/p.toml\"\n"
	cases := []struct {
		name, body, field string
		index             int
	}{
		{"missing name", src, "name", 0},
		{"blank name", "name = \"  \"\n" + src, "name", 0},
		{"both sources", "name = \"a\"\n" + src + "workspace = \"w\"\npath = \"p\"\n", "url", 0},
		{"neither source", "name = \"a\"\n", "url", 0},
		{"workspace without path", "name = \"a\"\nworkspace = \"w\"\n", "path", 0},
		{"path without workspace", "name = \"a\"\npath = \"p\"\n", "workspace", 0},
		{"plain http url", "name = \"a\"\nurl = \"http://example.com/p.toml\"\n", "url", 0},
		{"url without host", "name = \"a\"\nurl = \"https://\"\n", "url", 0},
		{"bad max_age", "name = \"a\"\n" + src + "max_age = \"soon\"\n", "max_age", 0},
		{"zero max_age", "name = \"a\"\n" + src + "max_age = \"0s\"\n", "max_age", 0},
		{"negative max_age", "name = \"a\"\n" + src + "max_age = \"-1h\"\n", "max_age", 0},
		{"unknown key", "name = \"a\"\nurll = \"https://example.com\"\n", "urll", -1},
		{"sha256 is not a key", "name = \"a\"\n" + src + "sha256 = \"abc\"\n", "sha256", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := writeSatelleToml(t, root, ConfigName, "[[gate.command_policy]]\n"+tc.body)
			_, _, err := Load(p)
			var cpe *CommandPolicyError
			if !errors.As(err, &cpe) {
				t.Fatalf("err = %v, want *CommandPolicyError", err)
			}
			if cpe.Field != tc.field || cpe.Index != tc.index || cpe.Path != p {
				t.Fatalf("got %+v, want field %q index %d path %q", cpe, tc.field, tc.index, p)
			}
		})
	}
}

func TestLoadCommandPolicyDuplicateName(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, `
[[gate.command_policy]]
name = "cloud"
url = "https://example.com/a.toml"

[[gate.command_policy]]
name = "cloud"
workspace = "w"
path = "p"
`)
	_, _, err := Load(p)
	var cpe *CommandPolicyError
	if !errors.As(err, &cpe) || cpe.Index != 1 || cpe.Field != "name" {
		t.Fatalf("err = %v, want a duplicate name at subscription 1", err)
	}
	if !strings.Contains(err.Error(), "subscription 1 field name") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestLoadCommandPolicyValid(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, `
[[gate.command_policy]]
name = "shared"
workspace = "team"
path = "policy/cloud.toml"
max_age = "12h"

[[gate.command_policy]]
name = "local"
url = "http://127.0.0.1:9/p.toml"
`)
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	subs := cfg.Gate.CommandPolicies
	if len(subs) != 2 || !subs[0].IsCatalog() || subs[1].IsCatalog() {
		t.Fatalf("subs = %+v", subs)
	}
	if subs[0].Age() != 12*time.Hour || subs[1].Age() != DefaultCommandPolicyMaxAge {
		t.Fatalf("ages = %v, %v", subs[0].Age(), subs[1].Age())
	}
	if subs[0].Source() != "team:policy/cloud.toml" {
		t.Fatalf("source = %q", subs[0].Source())
	}
}

func TestLoadOverlayOnlyMalformedPolicy(t *testing.T) {
	root := t.TempDir()
	p := writeSatelleToml(t, root, ConfigName, "[gate]\n")
	local := writeSatelleToml(t, root, LocalConfigName, "[[gate.command_policy]]\nname = \"a\"\n")
	_, path, err := Load(p)
	var cpe *CommandPolicyError
	if !errors.As(err, &cpe) || cpe.Path != local || path != local {
		t.Fatalf("err = %v (path %q), want the overlay %q", err, path, local)
	}
}

func TestParseCommandPolicyList(t *testing.T) {
	good := "[[command_rule]]\npattern = \"**/aws s3 rm\"\naction = \"deny\"\nmessage = \"no\"\n\n" +
		"[[command_rule]]\npattern = \"**/gcloud ** delete\"\naction = \"warn\"\nmessage = \"careful\"\n"
	rules, err := ParseCommandPolicyList([]byte(good))
	if err != nil || len(rules) != 2 {
		t.Fatalf("rules = %v, err = %v", rules, err)
	}
	if len(rules[0].Tokens) == 0 {
		t.Fatal("rules come back compiled")
	}
	for name, body := range map[string]string{
		"not toml":        "<html>nope</html>",
		"empty":           "",
		"no rules":        "x = 1\n",
		"bad pattern":     "[[command_rule]]\npattern = \"a; b\"\naction = \"deny\"\nmessage = \"m\"\n",
		"unknown action":  "[[command_rule]]\npattern = \"a\"\naction = \"block\"\nmessage = \"m\"\n",
		"no message":      "[[command_rule]]\npattern = \"a\"\naction = \"deny\"\n",
		"unknown key":     "[[command_rule]]\npattern = \"a\"\naction = \"deny\"\nmessage = \"m\"\nalow = [\"x\"]\n",
		"stray top level": good + "\n[gate]\nhandoff = \"off\"\n",
		"one good, one bad": "[[command_rule]]\npattern = \"a\"\naction = \"deny\"\nmessage = \"m\"\n" +
			"[[command_rule]]\npattern = \"\"\naction = \"deny\"\nmessage = \"m\"\n",
	} {
		if r, err := ParseCommandPolicyList([]byte(body)); err == nil || r != nil {
			t.Errorf("%s: rules = %v, err = %v, want a refusal", name, r, err)
		}
	}
}
