package agentstep

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A repo binds the performer and the reviewers of its story readiness step
// (propose) and of its container readiness step (waits_on_children) to an
// OpenRouter model through agents.toml and step.toml alone: this drives both
// steps through the engine against a fake `pi` on PATH, with the unmodified
// dispatch and transport code, and reads back what pi was started with.

const (
	openRouterModel = "openrouter/minimax/minimax-m3"
	openRouterTools = "read,grep,find,ls"
)

var openRouterWF = wfDoc(`["*"]
obligations = ["raised", "planned", "closed"]

[epic-parent]
obligations = ["raised", "ready", "closed-epic"]
`, `[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner-openrouter"
propose = true
skills = ["plan"]
reviewers = ["story-review"]
reviewer_agent = "reviewer-openrouter"
requires = ["raised"]

[ready]
status = "ready"
agent = "planner-openrouter"
propose = true
waits_on_children = true
skills = ["plan"]
reviewers = ["container-review"]
reviewer_agent = "reviewer-openrouter"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["planned"]

[closed-epic]
status = "done"
terminal = true
requires = ["ready"]
`)

func openRouterBindings(name string) (config.AgentBinding, bool) {
	b := config.AgentBinding{
		Command: "pi -p --provider openrouter --tools {tools} --model {model} {payload}",
		Tools:   openRouterTools, Model: openRouterModel, Auth: config.AuthAPIKey,
	}
	switch name {
	case "planner-openrouter":
		b.Role = config.RoleAgent
	case "reviewer-openrouter":
		b.Role = config.RoleReviewer
	default:
		return config.AgentBinding{}, false
	}
	return b, true
}

// fakePi writes an executable named pi that records the provider, tools and model
// flags of every start, and answers an accepting verdict.
func fakePi(t *testing.T) (marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "spawns")
	script := `#!/bin/sh
cat > /dev/null
out=
prev=
for a in "$@"; do
  case "$prev" in --provider|--tools|--model) out="$out $prev=$a";; esac
  prev=$a
done
echo "spawn$out" >> ` + marker + `
printf '%s' '{"decision":"accept","notes":"ok"}'
`
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	return marker
}

func TestOpenRouterReadinessSeatsCarryTheGrantAndModel(t *testing.T) {
	marker := fakePi(t)
	docs := fakeDocs{workflow: openRouterWF, skillBody: "Judge or perform the work item.", skillFound: true}
	g := New(&fakeRunner{}, docs, t.TempDir(), "")
	g.SetNamedAgents(openRouterBindings)

	items := map[string]struct {
		item workitem.Item
		to   string
	}{
		"story":     {workitem.Item{ID: "sty_story", Status: "backlog", Category: "feature", AcceptanceCriteria: "1. a"}, "plan"},
		"container": {workitem.Item{ID: "sty_epic", Status: "backlog", Category: "epic-parent", AcceptanceCriteria: "1. a"}, "ready"},
	}
	for _, name := range []string{"story", "container"} {
		c := items[name]
		res, err := g.DispatchExecutor(context.Background(), c.item, c.to)
		if err != nil {
			t.Fatalf("%s performer: %v", name, err)
		}
		dec, err := g.Gate(context.Background(), c.item, c.to)
		if err != nil || !dec.Accept || len(dec.Reviewers) != 1 {
			t.Fatalf("%s reviewers: dec=%+v err=%v", name, dec, err)
		}
		rv := dec.Reviewers[0]

		// AC3: the row names the model, and pi's usage — here unavailable, since the
		// fake wrote no session record — is named as pi's.
		if res.Model != openRouterModel || rv.Model != openRouterModel {
			t.Errorf("%s: model = %q / %q, want %q", name, res.Model, rv.Model, openRouterModel)
		}
		for who, n := range map[string]struct{ adapter, reason, auth string }{
			"performer": {res.Adapter, res.UsageUnavailableReason, res.AuthMode},
			"reviewer":  {rv.Adapter, rv.UsageUnavailableReason, rv.AuthMode},
		} {
			if n.adapter != agentcli.HarnessPi || !strings.HasPrefix(n.reason, "pi adapter:") {
				t.Errorf("%s %s: adapter=%q reason=%q, want a usage-unavailable reason naming the pi adapter", name, who, n.adapter, n.reason)
			}
			// AC5: the binding's auth rides the row.
			if n.auth != config.AuthAPIKey {
				t.Errorf("%s %s: auth_mode = %q, want %q", name, who, n.auth, config.AuthAPIKey)
			}
		}
	}

	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	spawns := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := "spawn --provider=openrouter --tools=" + openRouterTools + " --model=" + openRouterModel
	if len(spawns) != 4 {
		t.Fatalf("pi started %d times, want 4 (performer and reviewer on each of two steps): %q", len(spawns), spawns)
	}
	for i, s := range spawns {
		if s != want {
			t.Errorf("spawn %d = %q, want %q", i, s, want)
		}
	}
}
