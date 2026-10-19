package remotedef

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/health"
)

// scaffolded writes a valid definition under a temp data dir and returns the
// remote's directory.
func scaffolded(t *testing.T) string {
	t.Helper()
	dir := Dir(t.TempDir(), "r")
	if err := Scaffold(dir, "0.0.702"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	return dir
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceIn(t *testing.T, dir, rel, old, repl string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	write(t, dir, rel, strings.Replace(string(b), old, repl, 1))
}

func details(fs []health.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Detail)
	}
	sort.Strings(out)
	return out
}

// hasFinding reports whether some finding names file, then key, in that order.
func hasFinding(fs []health.Finding, file, key string) bool {
	for _, f := range fs {
		if f.Artifact == file && strings.HasPrefix(f.Detail, file+": "+key) {
			return true
		}
	}
	return false
}

func TestScaffoldIsValid(t *testing.T) {
	dir := scaffolded(t)
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("scaffold must validate clean, got:\n%s", strings.Join(details(fs), "\n"))
	}
	def, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if def.SatelleVersion != "0.0.702" || len(def.Segments) != 1 || def.Identity.Label == "" {
		t.Errorf("Load = %+v", def)
	}
	if err := Scaffold(dir, "0.0.702"); err == nil {
		t.Error("Scaffold must refuse a remote that already exists")
	}
}

// A host.run with no {command} cannot run a step; remote validate and remote test
// both report it from this one check.
func TestHostRunNeedsCommandPlaceholder(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", `run = ["your-host-cli", "run", "{label}", "{command}"]`, `run = ["your-host-cli", "run", "{label}"]`)
	fs := Validate(dir)
	if !hasFinding(fs, "remote.toml", "host.run") || len(fs) != 1 {
		t.Fatalf("want one host.run finding, got:\n%s", strings.Join(details(fs), "\n"))
	}
	if !strings.Contains(fs[0].Detail, "{command}") {
		t.Errorf("the finding must name the placeholder, got %q", fs[0].Detail)
	}
	// The placeholder may sit inside a larger argument.
	replaceIn(t, dir, "remote.toml", `run = ["your-host-cli", "run", "{label}"]`, `run = ["your-host-cli", "run", "--cmd={command}"]`)
	if fs := Validate(dir); len(fs) != 0 {
		t.Errorf("an embedded {command} must satisfy the check, got:\n%s", strings.Join(details(fs), "\n"))
	}
}

// TestAllErrorsReported puts independent defects in different files and keys and
// expects every one named in a single run.
func TestAllErrorsReported(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", `satelle_version = "0.0.702"`, `satelle_version = "1.2"`)
	replaceIn(t, dir, "remote.toml", `run = ["your-host-cli", "run", "{label}", "{command}"]`, `run = []`)
	replaceIn(t, dir, "remote.toml", `exit = "done"`, `exit = "elsewhere"`)
	// A category whose obligation no step discharges does not build; the working
	// lane still does, so the segment check has statuses to judge against.
	write(t, dir, "workflows/done.toml", `[meta]
name = "done"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "d"

["*"]
obligations = ["raised", "coded", "closed"]

[feature]
obligations = ["raised", "ghost"]
`)
	// A binding that is not a command template.
	replaceIn(t, dir, "workflows/agents.toml", `command = "your-agent-cli --system {system} --tools {tools}"`, `command = "your-agent-cli"`)

	fs := Validate(dir)
	for _, want := range []struct{ file, key string }{
		{"remote.toml", "satelle_version"},
		{"remote.toml", "host.run"},
		{"remote.toml", "segment[0].exit"},
		{"workflows/done.toml", "[feature]"},
		{"workflows/agents.toml", "worker"},
	} {
		if !hasFinding(fs, want.file, want.key) {
			t.Errorf("no finding for %s %s; got:\n%s", want.file, want.key, strings.Join(details(fs), "\n"))
		}
	}
	if len(fs) != 5 {
		t.Errorf("want exactly 5 findings, got %d:\n%s", len(fs), strings.Join(details(fs), "\n"))
	}
	for _, f := range fs {
		if f.Severity != health.SeverityError || f.ID != health.IDRemoteDefinition {
			t.Errorf("finding %+v must be a remote.definition error", f)
		}
	}
}

func TestUnparseableWorkflowIsNamed(t *testing.T) {
	dir := scaffolded(t)
	write(t, dir, "workflows/step.toml", "this is = = not toml")
	fs := Validate(dir)
	if !hasFinding(fs, "workflows/step.toml", "") {
		t.Fatalf("an unparseable step.toml must be named; got:\n%s", strings.Join(details(fs), "\n"))
	}
	// The workflow declares no statuses, so the segment is refused rather than
	// silently passed.
	if !hasFinding(fs, "remote.toml", "segment[0].entry") {
		t.Errorf("segment must be judged against the (empty) workflow; got:\n%s", strings.Join(details(fs), "\n"))
	}
}

func TestMissingFilesAreFindings(t *testing.T) {
	dir := scaffolded(t)
	for _, rel := range []string{"remote.toml", "workflows/done.toml", "workflows/step.toml", "workflows/agents.toml"} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	fs := Validate(dir)
	for _, rel := range []string{"remote.toml", "workflows/done.toml", "workflows/step.toml", "workflows/agents.toml"} {
		if !hasFinding(fs, rel, "") {
			t.Errorf("a missing %s must be a finding; got:\n%s", rel, strings.Join(details(fs), "\n"))
		}
	}
}

func TestUnknownKeyIsNamed(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", `wake_key =`, "satele_version = \"1.0.0\"\nwake_key =")
	if fs := Validate(dir); !hasFinding(fs, "remote.toml", "satele_version") {
		t.Fatalf("a misspelt key must be named; got:\n%s", strings.Join(details(fs), "\n"))
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load must refuse an unknown key")
	}
}

// TestIgnoresRepoWorkflows shows the remote's verdict is its own: a repo's
// .satelle/workflows beside the remote changes nothing, and a status only the
// repo declares is refused.
func TestIgnoresRepoWorkflows(t *testing.T) {
	data := t.TempDir()
	dir := Dir(data, "r")
	if err := Scaffold(dir, "0.0.702"); err != nil {
		t.Fatal(err)
	}
	validBefore := details(Validate(dir))
	// A repo workflow that would break a remote read from it, and declares a
	// status of its own.
	write(t, data, "workflows/done.toml", "not = = toml")
	write(t, data, "workflows/step.toml", "not = = toml")
	write(t, data, "workflows/agents.toml", "not = = toml")
	if got := details(Validate(dir)); strings.Join(got, "|") != strings.Join(validBefore, "|") {
		t.Errorf("a repo workflow changed a valid remote's verdict:\nbefore %v\nafter  %v", validBefore, got)
	}

	// The other way: a remote broken in its own files stays broken however good
	// the repo's workflow is, and a status that exists only in the repo is refused.
	write(t, data, "workflows/step.toml", `[meta]
name = "step"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "d"

[raised]
status = "backlog"
start = true

[repo-only]
status = "repo_only_status"
terminal = true
requires = ["raised"]
`)
	replaceIn(t, dir, "remote.toml", `exit = "done"`, `exit = "repo_only_status"`)
	fs := Validate(dir)
	if !hasFinding(fs, "remote.toml", "segment[0].exit") {
		t.Fatalf("a status only the repo workflow declares must be refused; got:\n%s", strings.Join(details(fs), "\n"))
	}
}

func TestCredentialRule(t *testing.T) {
	cases := []struct {
		name, edit string
		wantKey    string // "" means the definition must stay valid
	}{
		{"host env literal", "[host.env]\nAPI_TOKEN = \"abc\"\n", "host.env.API_TOKEN"},
		{"host env var reference", "[host.env]\nAPI_TOKEN = \"${API_TOKEN}\"\n", ""},
		{"nested trigger literal", "[trigger.params]\ntoken = \"abc\"\n", "trigger[0].params.token"},
		{"secrets are key names", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := scaffolded(t)
			b, err := os.ReadFile(filepath.Join(dir, "remote.toml"))
			if err != nil {
				t.Fatal(err)
			}
			body := string(b)
			if strings.HasPrefix(c.edit, "[trigger.params]") {
				body += c.edit
			} else if c.edit != "" {
				// Insert before the first array table so the new table is a
				// child of nothing the scaffold already closes.
				i := strings.Index(body, "# Lifecycle segments")
				body = body[:i] + c.edit + "\n" + body[i:]
			}
			write(t, dir, "remote.toml", body)
			fs := Validate(dir)
			if c.wantKey == "" {
				if len(fs) != 0 {
					t.Fatalf("must stay valid, got:\n%s", strings.Join(details(fs), "\n"))
				}
				return
			}
			if !hasFinding(fs, "remote.toml", c.wantKey) {
				t.Fatalf("want a finding for %s, got:\n%s", c.wantKey, strings.Join(details(fs), "\n"))
			}
			for _, f := range fs {
				if strings.Contains(f.Detail, "abc") {
					t.Errorf("a finding must never echo the value: %s", f.Detail)
				}
			}
		})
	}
}

func TestListOfTablesCredentialRefused(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", "[[trigger]]\nname = \"manual\"\nkind = \"manual\"\n", "[[trigger]]\nname = \"manual\"\nkind = \"manual\"\n\n[[trigger]]\nname = \"hooked\"\nkind = \"hook\"\n[trigger.params]\napi_key = \"abc\"\n")
	if fs := Validate(dir); !hasFinding(fs, "remote.toml", "trigger[1].params.api_key") {
		t.Fatalf("a literal in a later array table must be refused; got:\n%s", strings.Join(details(fs), "\n"))
	}
}

func TestKeyNames(t *testing.T) {
	good := scaffolded(t)
	replaceIn(t, good, "remote.toml", "secrets = []", `secrets = ["CLAUDE_CODE_OAUTH_TOKEN", "_X1"]`)
	if fs := Validate(good); len(fs) != 0 {
		t.Errorf("well-formed key names must be accepted, got:\n%s", strings.Join(details(fs), "\n"))
	}
	bad := scaffolded(t)
	replaceIn(t, bad, "remote.toml", "secrets = []", `secrets = ["lower", "A B"]`)
	replaceIn(t, bad, "remote.toml", `wake_key = "REMOTE_WAKE_KEY"`, `wake_key = "bad key"`)
	fs := Validate(bad)
	for _, key := range []string{"secrets[0]", "secrets[1]", "wake_key"} {
		if !hasFinding(fs, "remote.toml", key) {
			t.Errorf("want a finding for %s, got:\n%s", key, strings.Join(details(fs), "\n"))
		}
	}
}

func TestLabelIsOpaque(t *testing.T) {
	for _, label := range []string{"x", "team/ci runner:1", "any thing, at all"} {
		dir := scaffolded(t)
		replaceIn(t, dir, "remote.toml", `label = "your-host-label"`, `label = "`+label+`"`)
		if fs := Validate(dir); len(fs) != 0 {
			t.Errorf("label %q must be accepted, got:\n%s", label, strings.Join(details(fs), "\n"))
		}
	}
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", `label = "your-host-label"`, `label = " "`)
	if fs := Validate(dir); !hasFinding(fs, "remote.toml", "identity.label") {
		t.Errorf("an empty label must be refused; got:\n%s", strings.Join(details(fs), "\n"))
	}
}

// TestAgentsTransportRefusalsAreFindings: the remote's bindings are judged by the
// existing transport check, every refusal reported rather than the first.
func TestAgentsTransportRefusalsAreFindings(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "workflows/agents.toml", `tools = "read_file,grep,list_dir"`,
		"tools = \"read_file,grep,list_dir\"\nenv = { ANTHROPIC_API_KEY = \"sk-x\", OTHER_SECRET = \"y\" }")
	fs := Validate(dir)
	n := 0
	for _, f := range fs {
		if f.Artifact == "workflows/agents.toml" {
			n++
			if strings.Contains(f.Detail, "sk-x") {
				t.Errorf("finding echoes the value: %s", f.Detail)
			}
		}
	}
	if n < 2 {
		t.Errorf("want a finding per refusal (>=2), got %d:\n%s", n, strings.Join(details(fs), "\n"))
	}
	replaceIn(t, dir, "workflows/agents.toml", "env = {", "env = [")
	if fs := Validate(dir); !hasFinding(fs, "workflows/agents.toml", "parse") {
		t.Errorf("an undecodable agents.toml must be a finding; got:\n%s", strings.Join(details(fs), "\n"))
	}
}

func TestNames(t *testing.T) {
	data := t.TempDir()
	if names, err := Names(data); err != nil || len(names) != 0 {
		t.Fatalf("no remotes dir: %v %v", names, err)
	}
	for _, n := range []string{"b", "a"} {
		if err := Scaffold(Dir(data, n), "0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	names, _ := Names(data)
	if strings.Join(names, ",") != "a,b" {
		t.Errorf("Names = %v", names)
	}
	if !ValidName("my-remote_2") || ValidName("../x") || ValidName("A") || ValidName("") {
		t.Error("ValidName is wrong")
	}
}
