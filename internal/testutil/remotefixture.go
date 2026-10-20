package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The constants below describe one valid remote definition the remote test run
// is exercised on: two segments over a lane of three edges — backlog→plan (a
// performer, no reviewer), plan→in_progress (a performer and a reviewer) and
// in_progress→done (neither).
const (
	remoteFixtureMeta = `[meta]
name = %q
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "fixture"
`

	remoteFixtureDone = `["*"]
obligations = ["raised", "planned", "coded", "closed"]
`

	remoteFixtureStep = `
[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "worker"
skills = ["plan-work"]
requires = ["raised"]

[coded]
status = "in_progress"
agent = "worker"
skills = ["remote-work"]
reviewers = ["rev-skill"]
reviewer_agent = "reviewer"
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

	// RemoteFixtureWorkerCommand and RemoteFixtureReviewerCommand are the commands
	// the fixture's two bindings carry.
	RemoteFixtureWorkerCommand   = "fake-agent -p {system} --tools {tools}"
	RemoteFixtureReviewerCommand = "fake-reviewer -p {system} --tools {tools}"

	remoteFixtureAgents = `[worker]
role = "agent"
command = "` + RemoteFixtureWorkerCommand + `"
tools = "read_file,grep,list_dir"

[reviewer]
role = "reviewer"
command = "` + RemoteFixtureReviewerCommand + `"
tools = "Read,Grep,Glob"
`

	remoteFixtureSkill = `---
name: %s
type: skill
scope: project
tags: [type:skill]
description: fixture rubric
%s---

# %s

Do the work.
%s`

	remoteFixtureVerdict = `
## Verdict

Reply with JSON {"decision": "accept" or "reject", "notes": "why"}: the decision
field is accept|reject and notes carries your reasons.
`
)

// remoteFixtureToml is the fixture's remote.toml; %s is the host sentinel.
const remoteFixtureToml = `satelle_version = "1.2.3"
wake_key = "REMOTE_WAKE_KEY"

[identity]
label = "your-host-label"

[host]
start = ["your-host-cli", "start", "{label}"]
network = ["your-host-cli", "network", "{label}", "{allow}"]
run = ["touch", "%s", "your-host-cli", "run", "{label}", "{command}"]
stop = ["your-host-cli", "stop", "{label}"]

[[segment]]
entry = "backlog"
exit = "in_progress"

[[segment]]
entry = "in_progress"
exit = "done"

[[trigger]]
name = "manual"
kind = "manual"
`

// WriteRemoteFixture writes a valid remote definition, the fixture above, into
// dir under identity label "your-host-label". hostSentinel is a path the
// fixture's host.run command would create, so a test can prove no host command
// was executed; it must not contain "{command}".
func WriteRemoteFixture(t testing.TB, dir, hostSentinel string) {
	t.Helper()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("workflows/done.toml", fmt.Sprintf(remoteFixtureMeta, "done")+remoteFixtureDone)
	write("workflows/step.toml", fmt.Sprintf(remoteFixtureMeta, "step")+remoteFixtureStep)
	write("workflows/agents.toml", remoteFixtureAgents)
	write("skills/plan-work.md", fmt.Sprintf(remoteFixtureSkill, "plan-work", "", "plan-work", ""))
	write("skills/remote-work.md", fmt.Sprintf(remoteFixtureSkill, "remote-work",
		"output_name: ac-evidence\noutput_type: output\noutput_required: true\n", "remote-work", ""))
	write("skills/rev-skill.md", fmt.Sprintf(remoteFixtureSkill, "rev-skill", "", "rev-skill", remoteFixtureVerdict))
	write("remote.toml", fmt.Sprintf(remoteFixtureToml, hostSentinel))
}
