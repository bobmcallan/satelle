package config

import (
	"strings"
	"testing"
)

func TestRedactVarsStripsVarsKeepsProfiles(t *testing.T) {
	src := []byte(`# header comment

[vars]
# secret on this machine
GLM_API_KEY = "sk-secret-value"
OTHER = "x"

[profiles.claude-opus]
role       = "reviewer"
command    = "claude -p {system}"
tools      = "Read,Grep"

[roles]
# reviewer = "claude-opus"
`)
	got := RedactVars(src)
	s := string(got)
	if strings.Contains(s, "sk-secret-value") || strings.Contains(s, "GLM_API_KEY") {
		t.Fatalf("vars values must be absent after redaction:\n%s", s)
	}
	if !strings.Contains(s, "redacted from personal backup") {
		t.Fatalf("expected marker comment:\n%s", s)
	}
	if !strings.Contains(s, "[profiles.claude-opus]") || !strings.Contains(s, `command    = "claude -p {system}"`) {
		t.Fatalf("profile section must survive byte-exact:\n%s", s)
	}
	if !strings.Contains(s, "[roles]") {
		t.Fatalf("roles section must survive:\n%s", s)
	}
	// Result must still parse as a catalog.
	cfg, err := ParseGlobalAgents(string(got))
	if err != nil {
		t.Fatalf("ParseGlobalAgents after redaction: %v\n%s", err, s)
	}
	if len(cfg.Vars) != 0 {
		t.Errorf("parsed Vars must be empty, got %v", cfg.Vars)
	}
	if _, ok := cfg.Profiles["claude-opus"]; !ok {
		t.Errorf("profile claude-opus missing: %+v", cfg.Profiles)
	}
}

func TestRedactVarsNoVarsIsIdentity(t *testing.T) {
	src := []byte(`[profiles.p]
role = "reviewer"
command = "claude -p {system}"
`)
	got := RedactVars(src)
	if string(got) != string(src) {
		// Allow trailing newline normalisation only
		if strings.TrimSpace(string(got)) != strings.TrimSpace(string(src)) {
			t.Fatalf("without [vars], content should be unchanged:\ngot:\n%s\nwant:\n%s", got, src)
		}
	}
}

func TestRedactVarsEmpty(t *testing.T) {
	if got := RedactVars(nil); len(got) != 0 {
		t.Errorf("nil → empty, got %q", got)
	}
	if got := RedactVars([]byte{}); len(got) != 0 {
		t.Errorf("empty → empty, got %q", got)
	}
}
