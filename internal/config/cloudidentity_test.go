package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/testutil"
)

const launchCatalog = `
[profiles.ops]
role = "agent"

[profiles.ops.launch]
set = { CFG_DIR = "${SATELLE_PROFILE_DIR}/cfg" }
unset = ["AMBIENT_TOKEN"]

[[profiles.ops.launch.steps]]
run = ["first", "--dir", "${SATELLE_PROFILE_DIR}/one"]
env = { STEP_DIR = "${SATELLE_PROFILE_DIR}/one" }
secrets = { TOK = "${OPS_TOKEN}" }

[[profiles.ops.launch.steps]]
run = ["second"]
`

func mustCatalog(t *testing.T, body string) GlobalAgentsConfig {
	t.Helper()
	g, err := ParseGlobalAgents(body)
	if err != nil {
		t.Fatalf("ParseGlobalAgents: %v", err)
	}
	return g
}

func effectiveFor(t *testing.T, g GlobalAgentsConfig, repoAgents string) EffectiveAgents {
	t.Helper()
	repo, err := loadAgentsBody(repoAgents)
	if err != nil {
		t.Fatalf("loadAgentsBody: %v", err)
	}
	eff, err := ResolveEffectiveAgents(repo, g, nil)
	if err != nil {
		t.Fatalf("ResolveEffectiveAgents: %v", err)
	}
	return eff
}

// profile= on the executor seat keeps it in-loop, and the resolver reads the
// profile's launch table (not its command).
func TestInLoopLaunchSteps_SelectsForInLoopSeatWithProfile(t *testing.T) {
	testutil.IsolateHome(t)
	g := mustCatalog(t, launchCatalog)
	eff := effectiveFor(t, g, "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n")
	if !IsInLoopCommand(eff.Agents.ExecutorBinding().Command) {
		t.Fatalf("executor with profile= is no longer in-loop: %q", eff.Agents.ExecutorBinding().Command)
	}
	sel, ok, err := InLoopLaunchSteps(eff, g, map[string]string{"OPS_TOKEN": "tok-value"})
	if err != nil || !ok {
		t.Fatalf("selection = (%+v, %v, %v), want launch steps", sel, ok, err)
	}
	if sel.Profile != "ops" || sel.Dir != ProfileDir("ops") {
		t.Errorf("profile/dir = %q/%q", sel.Profile, sel.Dir)
	}
	if len(sel.Steps) != 2 {
		t.Fatalf("steps = %+v, want two", sel.Steps)
	}
	one := sel.Steps[0]
	if strings.Join(one.Run, " ") != "first --dir "+sel.Dir+"/one" || one.Env["STEP_DIR"] != sel.Dir+"/one" {
		t.Errorf("${SATELLE_PROFILE_DIR} not expanded in step 0: %+v", one)
	}
	if one.Secrets["TOK"] != "tok-value" {
		t.Errorf("secret not expanded from [vars]: %v", one.Secrets)
	}
	if sel.Set["CFG_DIR"] != sel.Dir+"/cfg" || len(sel.Unset) != 1 || sel.Unset[0] != "AMBIENT_TOKEN" {
		t.Errorf("set/unset = %v / %v", sel.Set, sel.Unset)
	}
	// No secret value may sit in anything bound for the session's environment.
	for k, v := range sel.Set {
		if strings.Contains(v, "tok-value") {
			t.Errorf("set %s carries a secret", k)
		}
	}
}

func TestInLoopLaunchSteps_NoSelection(t *testing.T) {
	g := mustCatalog(t, launchCatalog+"\n[profiles.plain]\nrole = \"agent\"\n[profiles.empty]\nrole = \"agent\"\n[profiles.empty.launch]\n")
	cases := []struct{ name, agents string }{
		{"no profile", "[executor]\ncommand = \"in-loop\"\n"},
		{"profile without launch", "[executor]\ncommand = \"in-loop\"\nprofile = \"plain\"\n"},
		{"empty launch table", "[executor]\ncommand = \"in-loop\"\nprofile = \"empty\"\n"},
		{"seat not in-loop", "[executor]\ncommand = \"claude -p {system}\"\nprofile = \"ops\"\n"},
		{"empty repo agents", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff := effectiveFor(t, g, c.agents)
			if sel, ok, err := InLoopLaunchSteps(eff, g, nil); ok || err != nil {
				t.Fatalf("selection = (%+v, %v, %v), want none", sel, ok, err)
			}
		})
	}
}

// A profile that extends another inherits its launch table.
func TestInLoopLaunchSteps_ExtendedProfile(t *testing.T) {
	testutil.IsolateHome(t)
	g := mustCatalog(t, launchCatalog+"\n[profiles.child]\nprofile = \"ops\"\n")
	eff := effectiveFor(t, g, "[executor]\ncommand = \"in-loop\"\nprofile = \"child\"\n")
	sel, ok, err := InLoopLaunchSteps(eff, g, map[string]string{"OPS_TOKEN": "t"})
	if err != nil || !ok || sel.Profile != "child" || len(sel.Steps) != 2 {
		t.Fatalf("selection = (%+v, %v, %v), want the inherited steps under child", sel, ok, err)
	}
}

// The top tier's launch table replaces the base's whole: steps are ordered, so
// merging two has no defined meaning.
func TestOverlayBindingReplacesLaunchWhole(t *testing.T) {
	base := AgentBinding{Launch: &LaunchSpec{Set: map[string]string{"A": "base"}, Steps: []LaunchStep{{Run: []string{"base"}}}}}
	top := AgentBinding{Launch: &LaunchSpec{Steps: []LaunchStep{{Run: []string{"top"}}}}}
	out, src := overlayBinding(base, map[string]string{"launch": "B"}, top, "T")
	if len(out.Launch.Steps) != 1 || out.Launch.Steps[0].Run[0] != "top" || len(out.Launch.Set) != 0 {
		t.Errorf("launch = %+v, want the top table alone", out.Launch)
	}
	if src["launch"] != "T" {
		t.Errorf("launch source = %q, want T", src["launch"])
	}
	if base.Launch.Steps[0].Run[0] != "base" {
		t.Error("overlay mutated the base binding")
	}
	if out, _ := overlayBinding(base, nil, AgentBinding{}, "T"); out.Launch != base.Launch {
		t.Error("a top tier with no launch table must keep the base's")
	}
}

// Secrets resolve from the machine [vars] and the gitignored local overlay
// only. A name defined solely in the committed repo [vars] is not a source.
func TestInLoopLaunchSteps_VarsSources(t *testing.T) {
	testutil.IsolateHome(t)
	g := mustCatalog(t, launchCatalog)
	eff := effectiveFor(t, g, "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n")

	_, ok, err := InLoopLaunchSteps(eff, g, nil)
	if !ok || err == nil || !strings.Contains(err.Error(), "${OPS_TOKEN}") {
		t.Fatalf("missing reference: ok=%v err=%v, want ok with an error naming ${OPS_TOKEN}", ok, err)
	}
	// The committed repo [vars] reaches eff.Vars but must not reach a secret.
	eff.Vars = LayerVars(g.Vars, map[string]string{"OPS_TOKEN": "from-committed"})
	if _, _, err := InLoopLaunchSteps(eff, g, nil); err == nil {
		t.Fatal("a reference defined only in the committed repo [vars] resolved")
	}
	// The catalog [vars] and the gitignored local overlay are sources.
	g.Vars = map[string]string{"OPS_TOKEN": "from-catalog"}
	if sel, ok, err := InLoopLaunchSteps(eff, g, nil); err != nil || !ok || sel.Steps[0].Secrets["TOK"] != "from-catalog" {
		t.Fatalf("catalog vars: (%+v, %v, %v)", sel, ok, err)
	}
	sel, ok, err := InLoopLaunchSteps(eff, g, map[string]string{"OPS_TOKEN": "from-local"})
	if err != nil || !ok || sel.Steps[0].Secrets["TOK"] != "from-local" {
		t.Fatalf("local overlay: (%+v, %v, %v), want the local value", sel, ok, err)
	}
}

func TestLaunchRefProblems(t *testing.T) {
	g := mustCatalog(t, launchCatalog)
	got := strings.Join(LaunchRefProblems(g, nil, map[string]string{"OPS_TOKEN": "committed"}), "\n")
	if !strings.Contains(got, "${OPS_TOKEN} is defined only in the committed repo [vars]") {
		t.Errorf("committed-only reference not reported:\n%s", got)
	}
	got = strings.Join(LaunchRefProblems(g, nil, nil), "\n")
	if !strings.Contains(got, "${OPS_TOKEN} is not defined") {
		t.Errorf("absent reference not reported:\n%s", got)
	}
	if p := LaunchRefProblems(g, map[string]string{"OPS_TOKEN": "x"}, nil); len(p) != 0 {
		t.Errorf("local overlay should satisfy the references, got %v", p)
	}
}

func TestLocalVarsAt_ReadsOnlyTheLocalOverlay(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, DefaultDataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, ConfigName), []byte("[vars]\nCOMMITTED = \"c\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, LocalConfigName), []byte("[vars]\nLOCAL = \"l\"\n"), 0o644)
	got := LocalVarsAt(root)
	if got["LOCAL"] != "l" || len(got) != 1 {
		t.Errorf("LocalVarsAt = %v, want only the local overlay", got)
	}
	if LocalVarsAt(t.TempDir()) != nil {
		t.Error("an absent overlay should be nil")
	}
}

// AC6: the starter catalog states the limits, points at the help topic, names no
// tool, and still parses (the commented section changes nothing).
func TestStarterGlobalAgents_LaunchStepText(t *testing.T) {
	s, err := StarterGlobalAgents("claude")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.ReplaceAll(s, "\n# ", " ")
	for _, want := range []string{
		"does not enforce rights or isolate secrets", "roles and policy", "separate OS user",
		"persist credentials in its own cache", "satelle help global-agents",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("starter catalog lacks %q", want)
		}
	}
	if _, err := ParseGlobalAgents(s); err != nil {
		t.Errorf("starter catalog does not parse: %v", err)
	}
}

// AC6: the help topic's examples are real configuration: each fenced
// [profiles.*.launch] block parses and validates.
func TestGlobalAgentsHelp_ExamplesValidate(t *testing.T) {
	topic, ok := help.Get("global-agents")
	if !ok {
		t.Fatal("global-agents help topic missing")
	}
	var blocks []string
	for _, part := range strings.Split(topic.Body, "```toml\n")[1:] {
		body, _, _ := strings.Cut(part, "```")
		if strings.Contains(body, "[profiles.ops-") && strings.Contains(body, ".launch") {
			blocks = append(blocks, body)
		}
	}
	if len(blocks) != 2 {
		t.Fatalf("want an Azure and an AWS example, found %d launch blocks", len(blocks))
	}
	for _, b := range blocks {
		g, err := ParseGlobalAgents(b)
		if err != nil {
			t.Fatalf("example does not validate: %v\n%s", err, b)
		}
		for name, p := range g.Profiles {
			if len(p.Launch.Steps) != 1 {
				t.Errorf("%s: want one step", name)
			}
			// The session reads the same dir the step signs in to.
			stepEnv, set := p.Launch.Steps[0].Env, p.Launch.Set
			shared := false
			for k, v := range stepEnv {
				if set[k] == v && strings.Contains(v, "${SATELLE_PROFILE_DIR}") {
					shared = true
				}
			}
			if !shared {
				t.Errorf("%s: set and the step's env do not share a ${SATELLE_PROFILE_DIR} path", name)
			}
		}
	}
}
