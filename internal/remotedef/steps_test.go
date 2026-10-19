package remotedef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appendTo adds text to the end of a file of the definition.
func appendTo(t *testing.T, dir, rel, text string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, rel, string(b)+text)
}

const wellFormedSteps = `
[preflight]
env_probe = ["your-env-probe"]
file_probe = ["your-file-probe"]
allow_credentials = ["SOME_KEY"]

[[step]]
name = "in_progress"
legs = ["data", "outbound"]

  [[step.probe]]
  argv = ["your-probe", "allowed"]
  expect = "succeed"

  [[step.probe]]
  argv = ["your-probe", "forbidden"]
  expect = "refused"
`

// A step declares probes with an expectation; a well-formed set validates clean
// and loads.
func TestStepProbesValidateAndLoad(t *testing.T) {
	dir := scaffolded(t)
	appendTo(t, dir, "remote.toml", wellFormedSteps)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("a well-formed preflight must validate clean, got:\n%s", strings.Join(details(fs), "\n"))
	}
	def, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Steps) != 1 || len(def.Steps[0].Probes) != 2 || def.Steps[0].Probes[1].Expect != ExpectRefused {
		t.Errorf("Steps = %+v", def.Steps)
	}
	if got := def.Preflight.AllowCredentials; len(got) != 1 || got[0] != "SOME_KEY" {
		t.Errorf("AllowCredentials = %v", got)
	}
}

// Validate is the only judge of probe form: each defect is a finding naming its key.
func TestStepDefectsAreFindings(t *testing.T) {
	cases := []struct {
		name, from, to, key string
	}{
		{"no expectation", `expect = "succeed"`, ``, "step[0].probe[0].expect"},
		{"unknown expectation", `expect = "succeed"`, `expect = "maybe"`, "step[0].probe[0].expect"},
		{"empty argv", `argv = ["your-probe", "allowed"]`, `argv = []`, "step[0].probe[0].argv"},
		{"unknown step", `name = "in_progress"`, `name = "nowhere"`, "step[0].name"},
		{"unknown leg", `legs = ["data", "outbound"]`, `legs = ["data", "egress"]`, "step[0].legs"},
		{"empty env probe", `env_probe = ["your-env-probe"]`, `env_probe = []`, "preflight.env_probe"},
		{"empty file probe", `file_probe = ["your-file-probe"]`, `file_probe = []`, "preflight.file_probe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := scaffolded(t)
			appendTo(t, dir, "remote.toml", strings.Replace(wellFormedSteps, tc.from, tc.to, 1))
			fs := Validate(dir)
			if !hasFinding(fs, "remote.toml", tc.key) {
				t.Fatalf("want a finding naming %s, got:\n%s", tc.key, strings.Join(details(fs), "\n"))
			}
		})
	}
}

func TestStepsHoldingAllLegs(t *testing.T) {
	def := Definition{Steps: []Step{
		{Name: "a", Legs: []string{LegData, LegUntrusted, LegOutbound}},
		{Name: "b", Legs: []string{LegData, LegOutbound}},
		{Name: "c"},
		{Name: "d", Legs: []string{LegOutbound, LegData, LegUntrusted}},
	}}
	got := def.StepsHoldingAllLegs()
	if len(got) != 2 || got[0] != "a" || got[1] != "d" {
		t.Errorf("StepsHoldingAllLegs = %v, want [a d]", got)
	}
}

func TestHostRunArgvFillsPlaceholders(t *testing.T) {
	def := Definition{Identity: Identity{Label: "lbl"}, Host: Host{Run: []string{"cli", "run", "{label}", "--cmd={command}"}}}
	if got := strings.Join(def.HostRunArgv("do it"), "|"); got != "cli|run|lbl|--cmd=do it" {
		t.Errorf("HostRunArgv = %q", got)
	}
}
