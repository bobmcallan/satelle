package remotedef

import (
	"fmt"
	"os"
	"path/filepath"
)

// scaffoldFile is one file of a new definition, relative to its directory.
type scaffoldFile struct{ rel, body string }

// Scaffold writes a minimal definition into dir, which must not exist: a remote
// pinned to version, with a one-lane workflow of its own, one binding, one skill
// and one principle. Every host command, label and agent command in it is a
// placeholder for the author to replace; it holds no value a platform would
// accept and no credential. The caller validates the result.
func Scaffold(dir, version string) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("remote already exists at %s", dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, f := range scaffoldFiles(version) {
		path := filepath.Join(dir, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f.body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func scaffoldFiles(version string) []scaffoldFile {
	return []scaffoldFile{
		{DefinitionFile, fmt.Sprintf(scaffoldRemoteToml, version)},
		{doneFile, scaffoldDone},
		{stepFile, scaffoldStep},
		{agentsFile, scaffoldAgents},
		{skillsDir + "/remote-work.md", scaffoldSkill},
		{principlesDir + "/remote-conduct.md", scaffoldPrinciple},
	}
}

const scaffoldRemoteToml = `# A remote definition: a defined version of satelle that runs somewhere else.
# Everything it needs is in this directory — its workflows, skills, principles and
# bindings are its own and never read the repo's. It holds no secret value:
# name .env keys (.satelle/config/.env), reference ${VAR}s, never paste a value.

# The satelle version this remote runs: MAJOR.MINOR.PATCH.
satelle_version = "%s"

# .env key names the remote's agents may read (names only, never values).
secrets = []

# .env key name of the credential used only to wake the node.
wake_key = "REMOTE_WAKE_KEY"

[identity]
# The identity label your platform understands; satelle treats it as opaque.
label = "your-host-label"

[host]
# Your own tooling for reaching the host, each an argv list. {label} is the
# identity label and {command} the command to run on the host.
start = ["your-host-cli", "start", "{label}"]
run = ["your-host-cli", "run", "{label}", "{command}"]
stop = ["your-host-cli", "stop", "{label}"]
# The command that works one story on the host; {story} is the story's id.
# story_command = "your-agent-cli {story}"

# Lifecycle segments this remote serves: an entry and an exit status, both
# statuses of this remote's own workflow (workflows/step.toml). Optionally
# categories = [...] narrows a segment to those story categories (none: every one).
[[segment]]
entry = "in_progress"
exit = "done"

# How the remote is started. name is unique and is what a fired trigger is
# addressed by; kind is opaque to satelle.
[[trigger]]
name = "manual"
kind = "manual"
`

const scaffoldDone = `[meta]
name = "done"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "This remote's declaration of done: one working lane every category follows. Half of a derived route; step.toml is the other half."

["*"]
obligations = ["raised", "coded", "closed"]
`

const scaffoldStep = `[meta]
name = "step"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "This remote's step catalogue: the steps the working lane selects, keyed by the obligation each discharges. Half of a derived route; done.toml is the other half."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "worker"
skills = ["remote-work"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

const scaffoldAgents = `# This remote's agent bindings. step.toml names a performer by section name and
# each [<name>] section says what it runs. Replace the command with your agent's
# full command line; keep credentials out — name .env keys in remote.toml instead.
[worker]
role = "agent"
command = "your-agent-cli --system {system} --tools {tools}"
tools = "read_file,grep,list_dir"
`

const scaffoldSkill = `---
name: remote-work
type: skill
scope: project
tags: [type:skill]
description: The rubric the remote's worker follows when it performs the coded step.
---

# Remote work

Implement the work item you are given and report what you changed. Replace this
text with the rubric your remote's agent should follow.
`

const scaffoldPrinciple = `---
name: remote-conduct
type: principle
tags: [principles:session]
description: How an agent on this remote conducts itself.
---

# Remote conduct

Work only inside the story you were handed, and never ask for a credential: use
the keys this remote's definition names. Replace this text with the principles
your remote should carry.
`
