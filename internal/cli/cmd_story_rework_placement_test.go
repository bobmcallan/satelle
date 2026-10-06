package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// Rework on a remote-placed child (sty_dde8b6a4): the step declares a rework
// loop AND a remote placement, so whether the relay may open depends on where
// the child runs. A remote child is refused — a cloud session cannot be a relay
// partner — and told to re-present the edge; a child placed local, including by
// the not-signed-in fallback, still opens the relay.

// placedReworkRepo is a repo with a parallel epic whose coded step is allocated
// to a live [coder] with a rework loop and a remote_agent, and one child of that
// epic engaged at in_progress by the local coder (the session is not signed in).
func placedReworkRepo(t *testing.T, childTags ...string) (id string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := tempRepo(t)
	t.Chdir(repo)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir,
		`["*"]
obligations = ["raised", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "parent-closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
requires = ["raised"]
rework = { consult = "consultant", rounds = 3 }
remote_agent = "coder-cloud"
local_tags = ["lane:trunk"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[ready]
status = "ready"
waits_on_children = true
schedule = "parallel"
requires = ["raised"]

[parent-closed]
status = "done"
agent = "reviewer"
terminal = true
requires = ["ready"]
`)
	coderPeer := filepath.Join(t.TempDir(), "fake-coder")
	consultPeer := filepath.Join(t.TempDir(), "fake-consultant")
	reworkPeer(t, coderPeer, "E2E_CODER_REPLIES")
	reworkPeer(t, consultPeer, "E2E_CONSULT_REPLIES")
	agents := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[orchestrator]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n" +
		"[coder]\nrole = \"agent\"\ninterface = \"stream\"\n" +
		"command = \"" + coderPeer + " --output-format stream-json\"\n" +
		"tools = \"Read,Grep,Glob,Edit,Write,Bash(satelle:*)\"\n\n" +
		"[coder-cloud]\nrole = \"agent\"\ninterface = \"cloud\"\ncommand = \"claude -p {system}\"\n\n" +
		"[consultant]\nrole = \"reviewer\"\ninterface = \"stream\"\n" +
		"command = \"" + consultPeer + " --output-format stream-json\"\n" +
		"tools = \"Read,Grep,Glob\"\nisolation = \"operator-attested\"\n"
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))
	t.Setenv("E2E_CONSULT_REPLIES", "READY")
	t.Setenv("E2E_CODER_REPLIES", "done")

	if out, err := runRoot(t, "story", "create", "--title", "the epic", "--body", "container",
		"--acceptance", "1. children close", "--category", "epic-parent", "--tags", "epic:w"); err != nil {
		t.Fatalf("create epic: %v\n%s", err, out)
	}
	tags := strings.Join(append([]string{"epic:w"}, childTags...), ",")
	out, err := runRoot(t, "story", "create", "--title", "a parallel child", "--body", "child",
		"--acceptance", "1. the relay is placement-aware", "--category", "chore", "--tags", tags)
	if err != nil {
		t.Fatalf("create child: %v\n%s", err, out)
	}
	id = jsonField(t, out, "id")
	if out, err := runRoot(t, "story", "set", id, "--status", "in_progress"); err != nil {
		t.Fatalf("engage child: %v\n%s", err, out)
	}
	return id
}

func jsonField(t *testing.T, raw, key string) string {
	t.Helper()
	i := strings.Index(raw, `"`+key+`"`)
	if i < 0 {
		t.Fatalf("no %q in %s", key, raw)
	}
	rest := raw[i+len(key)+2:]
	rest = rest[strings.Index(rest, `"`)+1:]
	return rest[:strings.Index(rest, `"`)]
}

func signIn(t *testing.T) {
	t.Helper()
	saveCred(t, config.DefaultHostedServer, hosted.Credential{PrincipalID: "P1"})
}

// AC6: a child the placement rule places remote is refused, the refusal names
// the placement and the way forward, and it is ledgered; status does not move.
func TestStoryReworkRefusesARemotePlacedChild(t *testing.T) {
	id := placedReworkRepo(t)
	signIn(t)

	out, err := runRoot(t, "story", "rework", id)
	if err == nil {
		t.Fatalf("rework must be refused for a remote-placed child:\n%s", out)
	}
	for _, want := range []string{id, "placed remote", "coder-cloud", "re-present the edge"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q, got: %v", want, err)
		}
	}
	if got := storyStatus(t, id); got != "in_progress" {
		t.Errorf("status = %q; a refusal must not move the story", got)
	}
	led, lerr := runRoot(t, "ledger", "list", "--story", id)
	if lerr != nil {
		t.Fatalf("ledger: %v\n%s", lerr, led)
	}
	if !strings.Contains(led, `"kind": "rework_refused"`) || !strings.Contains(led, `"placement": "remote"`) {
		t.Errorf("the refusal must be on the ledger:\n%s", led)
	}
	if rows := reworkMessages(t, id); len(rows) != 0 {
		t.Errorf("a refused relay must exchange no turns, got %+v", rows)
	}
}

// AC6: the same step's child that is not signed in — the not-signed-in
// fallback — is performed locally, so the relay still opens.
func TestStoryReworkOpensForANotSignedInChild(t *testing.T) {
	id := placedReworkRepo(t)

	out, err := runRoot(t, "story", "rework", id)
	if err != nil {
		t.Fatalf("a locally placed child must open the relay: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"converged":true`) {
		t.Errorf("relay result missing:\n%s", out)
	}
}

// AC6: a child carrying a local tag is placed local even when signed in.
func TestStoryReworkOpensForALocalTaggedChild(t *testing.T) {
	id := placedReworkRepo(t, "lane:trunk")
	signIn(t)

	out, err := runRoot(t, "story", "rework", id)
	if err != nil {
		t.Fatalf("a local-tagged child must open the relay: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"converged":true`) {
		t.Errorf("relay result missing:\n%s", out)
	}
}
