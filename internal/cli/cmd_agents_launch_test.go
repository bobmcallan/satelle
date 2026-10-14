package cli

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Unique sentinels for the long-lived secrets: finding one where it must not be
// is a leak.
const (
	sentAzureSecret = "SENTINEL-AZ-SECRET-c0ffee01"
	sentAWSKeyID    = "SENTINEL-AWS-KEYID-c0ffee02"
	sentAWSSecret   = "SENTINEL-AWS-SECRET-c0ffee03"
)

const launchCatalog = `
[vars]
OPS_AZ_SECRET = "` + sentAzureSecret + `"
OPS_AWS_KEY = "` + sentAWSKeyID + `"
OPS_AWS_SECRET = "` + sentAWSSecret + `"

[profiles.ops]
role = "agent"
command = "in-loop"

[profiles.ops.cloud.azure]
tenant = "tenant-1"
client_id = "client-1"
client_secret = "${OPS_AZ_SECRET}"
subscription = "sub-1"

[profiles.ops.cloud.aws]
access_key_id = "${OPS_AWS_KEY}"
secret_access_key = "${OPS_AWS_SECRET}"
region = "ap-southeast-2"
`

// launchFixture is a repo whose machine-local agents layer selects the ops
// profile, a machine catalog carrying its identity, and fake az/aws/claude/grok
// on PATH. launchExec runs the harness synchronously (no process is replaced and
// none is left behind), so the test sees the final argv and environment.
type launchFixture struct {
	repo, home, binDir string
	logs               map[string]string // program → its recorded argv + env
}

func newLaunchFixture(t *testing.T, catalog, localAgents string) *launchFixture {
	t.Helper()
	home := testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	t.Setenv(config.LauncherEnv, "")
	t.Setenv(config.CloudProfileEnv, "")
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

	f := &launchFixture{repo: repo, home: home, binDir: t.TempDir(), logs: map[string]string{}}
	for _, prog := range []string{"az", "aws", "claude", "grok"} {
		log := filepath.Join(t.TempDir(), prog+".log")
		f.logs[prog] = log
		tail := "exit 0"
		if prog == "aws" {
			tail = `printf '%s' '{"Credentials":{"AccessKeyId":"ASIASESSION","SecretAccessKey":"sess-secret","SessionToken":"sess-token","Expiration":"2026-10-15T10:00:00Z"}}'`
		}
		script := "#!/bin/sh\n{ echo \"ARGV: $*\"; env; } >> \"" + log + "\"\n" + tail + "\n"
		write(filepath.Join(f.binDir, prog), script)
		if err := os.Chmod(filepath.Join(f.binDir, prog), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", f.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prev := launchExec
	launchExec = func(path string, argv, env []string) error {
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

const localOps = "[executor]\ncommand = \"in-loop\"\nprofile = \"ops\"\n"

// AC2 + AC5: a session started through the launcher runs as the profile's
// identity, for claude and for grok. The fake CLIs record exactly what satelle
// handed them.
func TestAgentsLaunch_RunsAsTheProfileIdentity(t *testing.T) {
	for _, harness := range []string{"claude", "grok"} {
		t.Run(harness, func(t *testing.T) {
			f := newLaunchFixture(t, launchCatalog, localOps)
			t.Setenv("AZURE_CLIENT_SECRET", "ambient-sp-secret")
			t.Setenv("AWS_ACCESS_KEY_ID", "ambient-aws-key")

			out, err := runRoot(t, "agents", "launch", harness, "--", "--resume", "abc")
			if err != nil {
				t.Fatalf("launch: %v\n%s", err, out)
			}
			az := f.log(t, "az")
			for _, want := range []string{
				"ARGV: login --service-principal -u client-1 --tenant tenant-1 -p " + sentAzureSecret,
				"ARGV: account set --subscription sub-1",
				"AZURE_CONFIG_DIR=" + filepath.Join(f.home, "cloud", "ops", "azure"),
			} {
				if !strings.Contains(az, want) {
					t.Errorf("fake az lacks %q:\n%s", want, az)
				}
			}
			cfg, err := os.ReadFile(filepath.Join(f.home, "cloud", "ops", "aws", "config"))
			if err != nil || !strings.Contains(string(cfg), "credential_process = satelle agents credential aws ops") {
				t.Errorf("aws config = %q (%v), want the credential_process", cfg, err)
			}
			h := f.log(t, harness)
			for _, want := range []string{
				"ARGV: --resume abc",
				"AZURE_CONFIG_DIR=" + filepath.Join(f.home, "cloud", "ops", "azure"),
				"AWS_CONFIG_FILE=" + filepath.Join(f.home, "cloud", "ops", "aws", "config"),
				"AWS_SHARED_CREDENTIALS_FILE=" + filepath.Join(f.home, "cloud", "ops", "aws", "credentials"),
				"AWS_PROFILE=satelle-ops",
				config.LauncherEnv + "=" + harness,
				config.CloudProfileEnv + "=ops",
			} {
				if !strings.Contains(h, want) {
					t.Errorf("%s did not receive %q:\n%s", harness, want, h)
				}
			}
			// Ambient credentials outrank the selected profile in the providers'
			// own chains, so the adapters remove them.
			for _, gone := range []string{"AZURE_CLIENT_SECRET=", "AWS_ACCESS_KEY_ID="} {
				if strings.Contains(h, gone) {
					t.Errorf("%s still has ambient %s", harness, gone)
				}
			}
			if _, err := os.Stat(filepath.Join(f.repo, ".env")); err == nil {
				t.Error("a .env file exists in the repo")
			}
		})
	}
}

// AC3: the long-lived secrets never reach the launched agent's environment or
// argv, and satelle writes them nowhere — not the repo, not agents.toml, not its
// own files under the home, not what it printed.
func TestAgentsLaunch_NeverPlacesLongLivedSecrets(t *testing.T) {
	f := newLaunchFixture(t, launchCatalog, localOps)
	catalogPath := filepath.Join(f.home, config.GlobalAgentsName)
	before := sha256.Sum256(mustRead(t, catalogPath))

	out, err := runRoot(t, "agents", "launch", "claude", "--", "go")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, out)
	}
	sentinels := []string{sentAzureSecret, sentAWSKeyID, sentAWSSecret}

	agent := f.log(t, "claude")
	if agent == "" {
		t.Fatal("the harness never ran")
	}
	for _, s := range sentinels {
		if strings.Contains(agent, s) {
			t.Errorf("the agent's argv or environment carries %s:\n%s", s, agent)
		}
		if strings.Contains(out, s) {
			t.Errorf("launch printed %s:\n%s", s, out)
		}
	}
	// The azure secret is in the argv of the az login child alone, and that child
	// had exited before the harness started.
	if !strings.Contains(f.log(t, "az"), sentAzureSecret) {
		t.Error("expected the az login child to carry the secret in its own argv")
	}
	// AWS: the long-lived keys reach no child at launch; they are minted into
	// session credentials later, by credential_process.
	if f.log(t, "aws") != "" {
		t.Error("launch ran the aws CLI; minting belongs to credential_process")
	}

	if after := sha256.Sum256(mustRead(t, catalogPath)); before != after {
		t.Error("launch rewrote agents.toml")
	}
	// Everything satelle wrote: the repo tree, and the home apart from the
	// operator's own agents.toml and the cache the az CLI owns.
	for _, root := range []string{f.repo, f.home} {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || p == catalogPath {
				return nil
			}
			b, _ := os.ReadFile(p)
			for _, s := range sentinels {
				if strings.Contains(string(b), s) {
					t.Errorf("%s holds %s", p, s)
				}
			}
			return nil
		})
	}
}

// credential_process mints session credentials from the long-lived keys held in
// memory, prints them, and prints nothing else.
func TestAgentsCredentialAWS_PrintsSessionCredentialsOnly(t *testing.T) {
	f := newLaunchFixture(t, launchCatalog, localOps)
	stdout, stderr, err := runRootSplit(t, "", "agents", "credential", "aws", "ops")
	if err != nil {
		t.Fatalf("credential: %v\n%s", err, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc["Version"] != float64(1) {
		t.Fatalf("stdout must be exactly the credential_process JSON, got %q (%v)", stdout, err)
	}
	for _, want := range []string{`"Version":1`, `"AccessKeyId":"ASIASESSION"`, `"SessionToken":"sess-token"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %s: %q", want, stdout)
		}
	}
	for _, s := range []string{sentAWSKeyID, sentAWSSecret, sentAzureSecret} {
		if strings.Contains(stdout, s) || strings.Contains(stderr, s) {
			t.Errorf("credential printed %s", s)
		}
	}
	rec := f.log(t, "aws")
	if !strings.Contains(rec, "AWS_ACCESS_KEY_ID="+sentAWSKeyID) || strings.Contains(strings.SplitN(rec, "\n", 2)[0], sentAWSKeyID) {
		t.Errorf("long-lived keys must reach the aws child by environment only:\n%s", rec)
	}
	if err := filepath.Walk(f.home, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && p != filepath.Join(f.home, config.GlobalAgentsName) {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "sess-token") || strings.Contains(string(b), sentAWSSecret) {
				t.Errorf("credential wrote a credential to %s", p)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRootSplit(t, "", "agents", "credential", "aws", "missing"); err == nil {
		t.Error("credential for an unknown profile succeeded")
	}
	if _, _, err := runRootSplit(t, "", "agents", "credential", "azure", "ops"); err == nil {
		t.Error("credential for azure succeeded; only aws mints session credentials")
	}
}

// AC2: with no identity configured, outside a repo, or with repo configuration
// that cannot load, the launcher behaves as it always did: the harness starts
// with the parent's environment plus only the launcher marker, and no provider
// CLI runs.
func TestAgentsLaunch_NoIdentityBehavesAsToday(t *testing.T) {
	cases := []struct {
		name    string
		catalog string
		local   string
		prep    func(t *testing.T, f *launchFixture)
		warns   bool
	}{
		{name: "no profile named", catalog: launchCatalog, local: ""},
		{name: "profile without cloud", catalog: launchCatalog + "\n[profiles.plain]\nrole = \"agent\"\n", local: "[executor]\ncommand = \"in-loop\"\nprofile = \"plain\"\n"},
		{name: "no catalog", catalog: "", local: localOps, warns: true},
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
			stdout, stderr, err := runRootSplit(t, "", "agents", "launch", "claude", "--", "x")
			if err != nil {
				t.Fatalf("launch: %v\n%s", err, stderr)
			}
			_ = stdout
			if f.log(t, "az") != "" || f.log(t, "aws") != "" {
				t.Error("a provider CLI ran")
			}
			h := f.log(t, "claude")
			if !strings.Contains(h, "ARGV: x") || !strings.Contains(h, config.LauncherEnv+"=claude") {
				t.Errorf("harness did not start as launched:\n%s", h)
			}
			for _, absent := range []string{"AZURE_CONFIG_DIR=", "AWS_PROFILE=", "AWS_CONFIG_FILE=", config.CloudProfileEnv + "="} {
				if strings.Contains(h, absent) {
					t.Errorf("harness env carries %s with no identity configured", absent)
				}
			}
			if _, err := os.Stat(filepath.Join(f.home, "cloud")); err == nil {
				t.Error("a cloud state dir was created with no identity configured")
			}
			if c.warns && !strings.Contains(stderr, "cloud identity not applied") && c.name == "unloadable repo config" {
				t.Errorf("an unloadable repo config should say why no identity was applied:\n%s", stderr)
			}
		})
	}
}

// A named identity that cannot be resolved or signed in refuses to start: the
// harness never runs as the operator's own identity by accident.
func TestAgentsLaunch_RefusesWhenTheIdentityCannotBeUsed(t *testing.T) {
	t.Run("unresolved reference", func(t *testing.T) {
		f := newLaunchFixture(t, strings.Replace(launchCatalog, `OPS_AZ_SECRET = "`+sentAzureSecret+`"`, "", 1), localOps)
		_, err := runRoot(t, "agents", "launch", "claude")
		if err == nil || !strings.Contains(err.Error(), "${OPS_AZ_SECRET}") {
			t.Fatalf("err = %v, want a refusal naming the reference", err)
		}
		if f.log(t, "claude") != "" || f.log(t, "az") != "" {
			t.Error("something ran despite the refusal")
		}
	})
	t.Run("sign-in fails", func(t *testing.T) {
		f := newLaunchFixture(t, launchCatalog, localOps)
		fail := "#!/bin/sh\necho \"" + sentAzureSecret + "\" >&2\nexit 4\n"
		if err := os.WriteFile(filepath.Join(f.binDir, "az"), []byte(fail), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := runRoot(t, "agents", "launch", "claude")
		if err == nil || !strings.Contains(err.Error(), "az exited 4") {
			t.Fatalf("err = %v, want az exited 4", err)
		}
		if strings.Contains(err.Error(), sentAzureSecret) || strings.Contains(out, sentAzureSecret) {
			t.Error("the failure printed the secret")
		}
		if f.log(t, "claude") != "" {
			t.Error("the harness ran after a failed sign-in")
		}
	})
}

// AC5: pi and cursor have no launcher; the identity is recorded unavailable
// under the adapter's name, at install and at launch.
func TestAgentsLaunch_NoLauncherHarnessesRecordUnavailable(t *testing.T) {
	newLaunchFixture(t, launchCatalog, localOps)
	for _, h := range []string{"pi", "cursor"} {
		want := "cloud identity: unavailable (" + h + " has no satelle launcher)"
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

// AC6 (+AC3's per-provider persistence): the help says satelle selects and does
// not enforce, who does, and what each provider keeps.
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
		"Azure: the az CLI stores the service-principal secret in its cache under AZURE_CONFIG_DIR (~/.satelle/cloud/<profile>/azure)",
		"AWS: the agent receives only STS session credentials via credential_process",
		"satelle help global-agents",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("launch help lacks %q:\n%s", want, out)
		}
	}
	// The setup the help points at exists and names where the profile is selected.
	topic, ok := help.Get("global-agents")
	if !ok {
		t.Fatal("global-agents help topic missing")
	}
	for _, want := range []string{"## Cloud identity", "agents.local.toml", `${OPS_AZ_SECRET}`, "never from the committed `satelle.toml`"} {
		if !strings.Contains(topic.Body, want) {
			t.Errorf("global-agents topic lacks %q", want)
		}
	}
}

// AC1: `satelle agent validate` fails on a malformed identity or a literal
// secret, and on a reference no permitted [vars] source defines.
func TestAgentValidate_CloudIdentity(t *testing.T) {
	t.Run("literal secret", func(t *testing.T) {
		newLaunchFixture(t, strings.Replace(launchCatalog, `"${OPS_AZ_SECRET}"`, `"hunter2-literal"`, 1), "")
		out, err := runRoot(t, "agent", "validate")
		if err == nil || !strings.Contains(err.Error()+out, "literal secret") {
			t.Fatalf("err = %v, want a literal-secret failure:\n%s", err, out)
		}
		if strings.Contains(err.Error()+out, "hunter2-literal") {
			t.Error("validate echoed the literal")
		}
	})
	t.Run("reference defined only in the committed repo vars", func(t *testing.T) {
		f := newLaunchFixture(t, strings.Replace(launchCatalog, `OPS_AZ_SECRET = "`+sentAzureSecret+`"`, "", 1), "")
		cfg := filepath.Join(f.repo, config.DefaultDataDir, config.ConfigName)
		if err := os.WriteFile(cfg, []byte("[vars]\nOPS_AZ_SECRET = \"committed\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runRoot(t, "agent", "validate")
		if err == nil || !strings.Contains(out, "defined only in the committed repo [vars]") {
			t.Fatalf("err = %v, want the committed-vars failure:\n%s", err, out)
		}
		// The gitignored overlay is an accepted source.
		local := filepath.Join(f.repo, config.DefaultDataDir, config.LocalConfigName)
		if err := os.WriteFile(local, []byte("[vars]\nOPS_AZ_SECRET = \"local\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := runRoot(t, "agent", "validate"); err != nil {
			t.Fatalf("a local-overlay reference should validate: %v\n%s", err, out)
		}
	})
	t.Run("valid identity", func(t *testing.T) {
		newLaunchFixture(t, launchCatalog, localOps)
		if out, err := runRoot(t, "agent", "validate"); err != nil {
			t.Fatalf("a valid identity should validate: %v\n%s", err, out)
		}
	})
}

// AC1: the top-level `satelle validate` rejects the same broken identities as
// `agent validate` (one shared check), and never echoes the literal.
func TestValidateAll_CloudIdentity(t *testing.T) {
	t.Run("valid identity passes", func(t *testing.T) {
		newLaunchFixture(t, launchCatalog, localOps)
		out, err := runRoot(t, "validate")
		if err != nil || !strings.Contains(out, "PASS  cloud identities") {
			t.Fatalf("a valid identity should validate: %v\n%s", err, out)
		}
	})
	t.Run("literal secret", func(t *testing.T) {
		newLaunchFixture(t, strings.Replace(launchCatalog, `"${OPS_AZ_SECRET}"`, `"hunter2-literal"`, 1), "")
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
		f := newLaunchFixture(t, strings.Replace(launchCatalog, `OPS_AZ_SECRET = "`+sentAzureSecret+`"`, "", 1), "")
		cfg := filepath.Join(f.repo, config.DefaultDataDir, config.ConfigName)
		if err := os.WriteFile(cfg, []byte("[vars]\nOPS_AZ_SECRET = \"committed-literal\"\n"), 0o644); err != nil {
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
