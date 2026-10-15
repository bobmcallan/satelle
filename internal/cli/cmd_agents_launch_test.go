package cli

import (
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// A unique sentinel for the [vars] secret: finding it where it must not be is a
// leak.
const sentToken = "SENTINEL-OPS-TOKEN-c0ffee01"

// launchCatalog declares a profile with two steps. The first receives the
// secret and the profile dir; the second receives neither. The programs are fake
// scripts the fixture puts on PATH.
const launchCatalog = `
[vars]
OPS_TOKEN = "` + sentToken + `"

[profiles.ops]
role = "agent"
command = "in-loop"

[profiles.ops.launch]
set = { CFG_DIR = "${SATELLE_PROFILE_DIR}/cfg" }
unset = ["AMBIENT_TOKEN"]

[[profiles.ops.launch.steps]]
run = ["stepone", "--dir", "${SATELLE_PROFILE_DIR}/one"]
env = { STEP_DIR = "${SATELLE_PROFILE_DIR}/one" }
secrets = { TOK = "${OPS_TOKEN}" }

[[profiles.ops.launch.steps]]
run = ["steptwo"]
`

// useRealLaunchSteps lets the test run its fake step scripts for real, and
// restores the TestMain refusal afterwards.
func useRealLaunchSteps(t *testing.T) {
	t.Helper()
	prev := runLaunchStep
	runLaunchStep = realRunLaunchStep
	t.Cleanup(func() { runLaunchStep = prev })
}

// launchFixture is a repo whose machine-local agents layer selects the ops
// profile, a machine catalog declaring its steps, and fake stepone/steptwo/
// claude/grok on PATH. launchExec runs the harness synchronously (no process is
// replaced and none is left behind), so the test sees the final argv and
// environment.
type launchFixture struct {
	repo, home, binDir string
	order              string            // every fake program appends its name here, in run order
	got                string            // stepone writes the TOK it received here
	logs               map[string]string // program → its recorded argv, cwd and env
	execArgv, execEnv  []string          // what launchExec was handed
}

func newLaunchFixture(t *testing.T, catalog, localAgents string) *launchFixture {
	t.Helper()
	useRealLaunchSteps(t)
	home := testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	t.Setenv(config.LauncherEnv, "")
	t.Setenv(config.LaunchProfileEnv, "")
	repo := t.TempDir()
	wf := filepath.Join(repo, config.DefaultDataDir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, config.DefaultDataDir, config.ConfigName), "[review]\ngate_create = false\n")
	write(filepath.Join(wf, config.AgentsConfigName), "[executor]\ncommand = \"in-loop\"\n")
	if localAgents != "" {
		write(filepath.Join(wf, config.LocalAgentsName), localAgents)
	}
	if catalog != "" {
		write(filepath.Join(home, config.GlobalAgentsName), catalog)
	}

	scratch := t.TempDir()
	f := &launchFixture{repo: repo, home: home, binDir: t.TempDir(), logs: map[string]string{},
		order: filepath.Join(scratch, "order"), got: filepath.Join(scratch, "got")}
	for _, prog := range []string{"stepone", "steptwo", "claude", "grok"} {
		log := filepath.Join(scratch, prog+".log")
		f.logs[prog] = log
		extra := ""
		if prog == "stepone" {
			extra = "printf %s \"$TOK\" > \"" + f.got + "\"\n"
		}
		script := "#!/bin/sh\n{ echo \"ARGV: $*\"; echo \"PWD: $(pwd -P)\"; env; } >> \"" + log + "\"\n" +
			"echo " + prog + " >> \"" + f.order + "\"\n" + extra + "exit 0\n"
		write(filepath.Join(f.binDir, prog), script)
		if err := os.Chmod(filepath.Join(f.binDir, prog), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", f.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prev := launchExec
	launchExec = func(path string, argv, env []string) error {
		f.execArgv, f.execEnv = argv, env
		cmd := exec.Command(path, argv[1:]...)
		cmd.Env = env
		return cmd.Run()
	}
	t.Cleanup(func() { launchExec = prev })
	t.Chdir(repo)
	return f
}

func (f *launchFixture) log(t *testing.T, prog string) string {
	t.Helper()
	b, err := os.ReadFile(f.logs[prog])
	if err != nil {
		return ""
	}
	return string(b)
}

func (f *launchFixture) ran(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.order)
	return strings.Join(strings.Fields(string(b)), ",")
}

// replaceStep rewrites a fake program as a script body.
func (f *launchFixture) replaceStep(t *testing.T, prog, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.binDir, prog), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

const localOps = "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n"

// AC2: a session started through the launcher runs the profile's steps in
// declared order, each in the repo root with its own environment, then starts
// the harness with the session environment the profile declared.
func TestAgentsLaunch_RunsStepsInOrderThenTheHarness(t *testing.T) {
	for _, harness := range []string{"claude", "grok"} {
		t.Run(harness, func(t *testing.T) {
			f := newLaunchFixture(t, launchCatalog, localOps)
			t.Setenv("AMBIENT_TOKEN", "ambient-value")
			dir := filepath.Join(f.home, "profiles", "ops")

			out, err := runRoot(t, "agents", "launch", harness, "--", "--resume", "abc")
			if err != nil {
				t.Fatalf("launch: %v\n%s", err, out)
			}
			if got, want := f.ran(t), "stepone,steptwo,"+harness; got != want {
				t.Fatalf("run order = %s, want %s", got, want)
			}
			one := f.log(t, "stepone")
			repoReal, _ := filepath.EvalSymlinks(f.repo)
			for _, want := range []string{
				"ARGV: --dir " + dir + "/one",
				"PWD: " + repoReal,
				"STEP_DIR=" + dir + "/one",
				config.ProfileDirVar + "=" + dir,
				"TOK=" + sentToken,
			} {
				if !strings.Contains(one, want) {
					t.Errorf("step 0 lacks %q:\n%s", want, one)
				}
			}
			if two := f.log(t, "steptwo"); strings.Contains(two, "TOK=") || strings.Contains(two, "STEP_DIR=") {
				t.Errorf("step 1 received step 0's environment:\n%s", two)
			}
			h := f.log(t, harness)
			for _, want := range []string{
				"ARGV: --resume abc",
				"CFG_DIR=" + dir + "/cfg",
				config.ProfileDirVar + "=" + dir,
				config.LauncherEnv + "=" + harness,
				config.LaunchProfileEnv + "=ops",
			} {
				if !strings.Contains(h, want) {
					t.Errorf("%s did not receive %q:\n%s", harness, want, h)
				}
			}
			for _, gone := range []string{"AMBIENT_TOKEN=", "TOK=", "STEP_DIR="} {
				if strings.Contains(h, gone) {
					t.Errorf("%s still has %s", harness, gone)
				}
			}
			if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
				t.Errorf("profile dir = %v (%v), want mode 0700", st, err)
			}
			if _, err := os.Stat(filepath.Join(f.repo, ".env")); err == nil {
				t.Error("a .env file exists in the repo")
			}
		})
	}
}

// AC2: a failing step stops the launch with that step's own exit status and a
// message naming only the step index and command; the harness never starts.
func TestAgentsLaunch_FailingStepStopsTheLaunch(t *testing.T) {
	t.Run("step exits non-zero", func(t *testing.T) {
		f := newLaunchFixture(t, launchCatalog, localOps)
		f.replaceStep(t, "stepone", "echo stepone >> \""+f.order+"\"\nexit 4")
		out, err := runRoot(t, "agents", "launch", "claude")
		var lse *launchStepError
		if !errors.As(err, &lse) || lse.code != 4 {
			t.Fatalf("err = %v, want a launchStepError with exit status 4", err)
		}
		if err.Error() != "launch: step 0 (stepone) exited 4" {
			t.Errorf("message = %q, want only the step index and command name", err.Error())
		}
		if strings.Contains(err.Error()+out, sentToken) {
			t.Error("the failure printed the secret")
		}
		if got := f.ran(t); got != "stepone" {
			t.Errorf("ran %q, want only the failing step", got)
		}
		if f.log(t, "claude") != "" {
			t.Error("the harness ran after a failed step")
		}
	})
	t.Run("command cannot be started", func(t *testing.T) {
		f := newLaunchFixture(t, strings.Replace(launchCatalog, `"stepone"`, `"no-such-step-command"`, 1), localOps)
		_, err := runRoot(t, "agents", "launch", "claude")
		var lse *launchStepError
		if !errors.As(err, &lse) || lse.code != 127 || lse.index != 0 || lse.name != "no-such-step-command" {
			t.Fatalf("err = %v, want step 0 to exit 127", err)
		}
		if f.log(t, "claude") != "" {
			t.Error("the harness ran after a failed step")
		}
	})
}

// AC3: the [vars] secret reaches the step that asked for it and nothing else:
// not the harness's environment or argv, not the repo, not satelle's own files
// under the home, not what satelle printed, and the catalog is not rewritten.
func TestAgentsLaunch_NeverPlacesTheSecret(t *testing.T) {
	f := newLaunchFixture(t, launchCatalog, localOps)
	catalogPath := filepath.Join(f.home, config.GlobalAgentsName)
	before := sha256.Sum256(mustRead(t, catalogPath))

	stdout, stderr, err := runRootSplit(t, "", "agents", "launch", "claude", "--", "go")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, stderr)
	}
	// The step itself received it.
	if got := string(mustRead(t, f.got)); got != sentToken {
		t.Fatalf("step 0 received TOK = %q, want the sentinel", got)
	}
	if f.execEnv == nil || f.execArgv == nil {
		t.Fatal("the harness was never exec'd")
	}
	for name, hay := range map[string]string{
		"exec'd environment": strings.Join(f.execEnv, "\n"),
		"exec'd argv":        strings.Join(f.execArgv, "\n"),
		"harness record":     f.log(t, "claude"),
		"stdout":             stdout,
		"stderr":             stderr,
	} {
		if strings.Contains(hay, sentToken) {
			t.Errorf("%s carries the secret:\n%s", name, hay)
		}
	}
	// The catalog is untouched, and its only copy is the operator's own [vars]
	// entry; the profile section holds just the reference.
	catalog := string(mustRead(t, catalogPath))
	if after := sha256.Sum256([]byte(catalog)); before != after {
		t.Error("launch rewrote agents.toml")
	}
	if n := strings.Count(catalog, sentToken); n != 1 || !strings.Contains(catalog, "OPS_TOKEN = \""+sentToken+"\"") {
		t.Errorf("the secret appears %d times in agents.toml, want only the [vars] line", n)
	}
	// Everything satelle could have written: the repo tree and the whole home
	// (HOME holds the profile dir and any log), apart from the operator's catalog.
	for _, root := range []string{f.repo, f.home} {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || p == catalogPath {
				return nil
			}
			if b, _ := os.ReadFile(p); strings.Contains(string(b), sentToken) {
				t.Errorf("%s holds the secret", p)
			}
			return nil
		})
	}
}

// AC2: with no launch table, outside a repo, with no catalog, or with config
// that cannot load, the launcher behaves as it always did: the harness starts
// with the parent's environment plus only the launcher marker, and no step runs.
func TestAgentsLaunch_NoDeclarationBehavesAsToday(t *testing.T) {
	cases := []struct {
		name    string
		catalog string
		local   string
		prep    func(t *testing.T, f *launchFixture)
		warns   bool
	}{
		{name: "no profile named", catalog: launchCatalog, local: ""},
		{name: "profile without launch", catalog: launchCatalog + "\n[profiles.plain]\nrole = \"agent\"\n", local: "[executor]\ncommand = \"in-loop\"\nprofile = \"plain\"\n"},
		{name: "no catalog", catalog: "", local: localOps},
		{name: "outside a repo", catalog: launchCatalog, local: localOps, prep: func(t *testing.T, f *launchFixture) { t.Chdir(t.TempDir()) }},
		{name: "unloadable repo config", catalog: launchCatalog, local: localOps, warns: true, prep: func(t *testing.T, f *launchFixture) {
			if err := os.WriteFile(filepath.Join(f.repo, config.DefaultDataDir, config.ConfigName), []byte("[[[ not toml"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLaunchFixture(t, c.catalog, c.local)
			if c.prep != nil {
				c.prep(t, f)
			}
			_, stderr, err := runRootSplit(t, "", "agents", "launch", "claude", "--", "x")
			if err != nil {
				t.Fatalf("launch: %v\n%s", err, stderr)
			}
			if got := f.ran(t); got != "claude" {
				t.Errorf("ran %q, want only the harness", got)
			}
			h := f.log(t, "claude")
			if !strings.Contains(h, "ARGV: x") || !strings.Contains(h, config.LauncherEnv+"=claude") {
				t.Errorf("harness did not start as launched:\n%s", h)
			}
			for _, absent := range []string{"CFG_DIR=", config.ProfileDirVar + "=", config.LaunchProfileEnv + "="} {
				if strings.Contains(h, absent) {
					t.Errorf("harness env carries %s with nothing declared", absent)
				}
			}
			if _, err := os.Stat(filepath.Join(f.home, "profiles")); err == nil {
				t.Error("a profile dir was created with nothing declared")
			}
			if c.warns && !strings.Contains(stderr, "launch steps not applied") {
				t.Errorf("an unloadable config should say why no steps were applied:\n%s", stderr)
			}
		})
	}
}

// AC2: the composed plan with nothing declared is exactly today's: the parent's
// environment with only SATELLE_LAUNCHER added and the profile markers (this
// launch's and the 0.0.672 name) removed.
func TestPlanLaunch_NoDeclarationPlanEqualsToday(t *testing.T) {
	newLaunchFixture(t, launchCatalog, "")
	prev := launchEnv
	launchEnv = func() []string {
		return []string{"PATH=/bin", "KEEP=1", config.LaunchProfileEnv + "=stale", config.LegacyLaunchProfileEnv + "=stale"}
	}
	t.Cleanup(func() { launchEnv = prev })

	plan, err := planLaunch(t.Context(), "claude", []string{"a"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"PATH=/bin": true, "KEEP=1": true, config.LauncherEnv + "=claude": true}
	if len(plan.Env) != len(want) {
		t.Fatalf("env = %v, want exactly %v", plan.Env, want)
	}
	for _, kv := range plan.Env {
		if !want[kv] {
			t.Errorf("unexpected %q in %v", kv, plan.Env)
		}
	}
	if plan.Binary != "claude" || len(plan.Args) != 1 || plan.Args[0] != "a" {
		t.Errorf("plan = %+v", plan)
	}
}

// A declared secret that cannot be resolved refuses to start, naming the
// reference: the harness never runs without the steps it was configured with.
func TestAgentsLaunch_RefusesWhenAReferenceCannotBeResolved(t *testing.T) {
	f := newLaunchFixture(t, strings.Replace(launchCatalog, `OPS_TOKEN = "`+sentToken+`"`, "", 1), localOps)
	_, err := runRoot(t, "agents", "launch", "claude")
	if err == nil || !strings.Contains(err.Error(), "${OPS_TOKEN}") {
		t.Fatalf("err = %v, want a refusal naming the reference", err)
	}
	if got := f.ran(t); got != "" {
		t.Errorf("%q ran despite the refusal", got)
	}
}

// AC5: pi and cursor have no launcher; launch steps are recorded unavailable
// under the adapter's name, at install and at launch.
func TestAgentsLaunch_NoLauncherHarnessesRecordUnavailable(t *testing.T) {
	newLaunchFixture(t, launchCatalog, localOps)
	for _, h := range []string{"pi", "cursor"} {
		want := "launch steps: unavailable (" + h + " has no satelle launcher)"
		if _, err := runRoot(t, "agents", "launch", h); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("launch %s: err = %v, want %q", h, err, want)
		}
		rs, err := installLaunchers(config.GlobalDir(), h)
		if err != nil || len(rs) != 1 || rs[0].Note != want || rs[0].Path != "" {
			t.Errorf("install %s: %+v (%v), want one result noting %q", h, rs, err, want)
		}
	}
	rs, err := installLaunchers(t.TempDir(), "all")
	if err != nil || len(rs) != 4 {
		t.Fatalf("install all = %+v (%v), want claude, grok, pi, cursor", rs, err)
	}
	for _, r := range rs[2:] {
		if !strings.Contains(r.Note, "unavailable") {
			t.Errorf("%s: note %q", r.Name, r.Note)
		}
	}
}

// AC6: the launch help states the limits and points at the setup topic, which
// carries the schema and names where the profile is selected.
func TestAgentsLaunchHelpStatesTheLimits(t *testing.T) {
	out, err := runRoot(t, "agents", "launch", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"does not enforce rights or isolate secrets",
		"roles and policy",
		"separate OS user",
		"persist credentials in its own cache",
		"satelle help global-agents",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("launch help lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(flat, "as the Azure or AWS identity") {
		t.Error("launch help claims the launcher runs as a cloud identity")
	}
	topic, ok := help.Get("global-agents")
	if !ok {
		t.Fatal("global-agents help topic missing")
	}
	for _, want := range []string{"## Launch steps", "agents.local.toml", "${SATELLE_PROFILE_DIR}", "never from the committed `satelle.toml`"} {
		if !strings.Contains(topic.Body, want) {
			t.Errorf("global-agents topic lacks %q", want)
		}
	}
}

// AC1: `satelle agent validate` fails on a malformed declaration, a literal
// secret, a [vars] reference outside a step's secrets and a reference no
// permitted [vars] source defines, and never echoes a value.
func TestAgentValidate_LaunchSteps(t *testing.T) {
	t.Run("literal secret", func(t *testing.T) {
		newLaunchFixture(t, strings.Replace(launchCatalog, `"${OPS_TOKEN}"`, `"hunter2-literal"`, 1), "")
		out, err := runRoot(t, "agent", "validate")
		if err == nil || !strings.Contains(err.Error()+out, "literal secret") {
			t.Fatalf("err = %v, want a literal-secret failure:\n%s", err, out)
		}
		if strings.Contains(err.Error()+out, "hunter2-literal") {
			t.Error("validate echoed the literal")
		}
	})
	t.Run("vars reference outside secrets", func(t *testing.T) {
		newLaunchFixture(t, strings.Replace(launchCatalog, `run = ["steptwo"]`, `run = ["steptwo", "${OPS_TOKEN}"]`, 1), "")
		out, err := runRoot(t, "agent", "validate")
		if err == nil || !strings.Contains(err.Error()+out, "only a step's secrets may reference a [vars] entry") {
			t.Fatalf("err = %v, want the reference-rule failure:\n%s", err, out)
		}
		if strings.Contains(err.Error()+out, sentToken) {
			t.Error("validate echoed the secret")
		}
	})
	t.Run("legacy cloud table", func(t *testing.T) {
		newLaunchFixture(t, "[profiles.ops]\nrole = \"agent\"\n[profiles.ops.cloud.vendor]\nsecret = \"hunter2-literal\"\n", "")
		out, err := runRoot(t, "agent", "validate")
		if err == nil {
			t.Fatalf("a legacy cloud table validated:\n%s", out)
		}
		got := err.Error() + out
		if !strings.Contains(got, "[profiles.ops.launch]") || !strings.Contains(got, "satelle help global-agents") {
			t.Fatalf("err = %v, want a pointer to [profiles.ops.launch] and the help topic:\n%s", err, out)
		}
		if strings.Contains(got, "hunter2-literal") {
			t.Error("validate echoed the legacy value")
		}
	})
	t.Run("reference defined only in the committed repo vars", func(t *testing.T) {
		f := newLaunchFixture(t, strings.Replace(launchCatalog, `OPS_TOKEN = "`+sentToken+`"`, "", 1), "")
		cfg := filepath.Join(f.repo, config.DefaultDataDir, config.ConfigName)
		if err := os.WriteFile(cfg, []byte("[vars]\nOPS_TOKEN = \"committed\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runRoot(t, "agent", "validate")
		if err == nil || !strings.Contains(out, "defined only in the committed repo [vars]") {
			t.Fatalf("err = %v, want the committed-vars failure:\n%s", err, out)
		}
		// The gitignored overlay is an accepted source.
		local := filepath.Join(f.repo, config.DefaultDataDir, config.LocalConfigName)
		if err := os.WriteFile(local, []byte("[vars]\nOPS_TOKEN = \"local\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := runRoot(t, "agent", "validate"); err != nil {
			t.Fatalf("a local-overlay reference should validate: %v\n%s", err, out)
		}
	})
	t.Run("valid declaration", func(t *testing.T) {
		newLaunchFixture(t, launchCatalog, localOps)
		out, err := runRoot(t, "agent", "validate")
		if err != nil {
			t.Fatalf("a valid declaration should validate: %v\n%s", err, out)
		}
		if strings.Contains(out, "launch.set") {
			t.Errorf("warned about env with none declared:\n%s", out)
		}
	})
	t.Run("in-loop seat with env warns", func(t *testing.T) {
		catalog := launchCatalog + "\n[profiles.withenv]\nrole = \"agent\"\nenv = { ANTHROPIC_BASE_URL = \"https://x.invalid\" }\n"
		newLaunchFixture(t, catalog, strings.Replace(localOps, `"ops"`, `"withenv"`, 1))
		out, err := runRoot(t, "agent", "validate")
		if err != nil {
			t.Fatalf("a warning must not fail validate: %v\n%s", err, out)
		}
		if !strings.Contains(out, "WARN  profile withenv is the in-loop seat") || !strings.Contains(out, "launch.set") {
			t.Errorf("missing the in-loop env warning:\n%s", out)
		}
		if strings.Contains(out, "x.invalid") {
			t.Error("the warning echoed an env value")
		}
	})
}

// AC1: the top-level `satelle validate` rejects the same broken declarations as
// `agent validate` (one shared check), and never echoes the literal.
func TestValidateAll_LaunchSteps(t *testing.T) {
	t.Run("valid declaration passes", func(t *testing.T) {
		newLaunchFixture(t, launchCatalog, localOps)
		out, err := runRoot(t, "validate")
		if err != nil || !strings.Contains(out, "PASS  launch steps") {
			t.Fatalf("a valid declaration should validate: %v\n%s", err, out)
		}
	})
	t.Run("literal secret", func(t *testing.T) {
		newLaunchFixture(t, strings.Replace(launchCatalog, `"${OPS_TOKEN}"`, `"hunter2-literal"`, 1), "")
		out, err := runRoot(t, "validate")
		// The store-backed bootstrap (requireAgents) refuses a broken catalog
		// before the verb runs, so the failure may arrive as the error rather than
		// as a FAIL line; either way the exit is non-zero and names the field.
		if err == nil || !strings.Contains(err.Error()+out, "literal secret") {
			t.Fatalf("err = %v, want a literal-secret failure:\n%s", err, out)
		}
		if strings.Contains(err.Error()+out, "hunter2-literal") {
			t.Error("validate echoed the literal")
		}
	})
	t.Run("reference defined only in the committed repo vars", func(t *testing.T) {
		f := newLaunchFixture(t, strings.Replace(launchCatalog, `OPS_TOKEN = "`+sentToken+`"`, "", 1), "")
		cfg := filepath.Join(f.repo, config.DefaultDataDir, config.ConfigName)
		if err := os.WriteFile(cfg, []byte("[vars]\nOPS_TOKEN = \"committed-literal\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runRoot(t, "validate")
		if err == nil || !strings.Contains(out, "defined only in the committed repo [vars]") {
			t.Fatalf("err = %v, want the committed-vars failure:\n%s", err, out)
		}
		if strings.Contains(err.Error()+out, "committed-literal") {
			t.Error("validate echoed the committed value")
		}
	})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
