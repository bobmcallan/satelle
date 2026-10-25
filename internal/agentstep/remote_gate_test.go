package agentstep

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// A gate whose role=reviewer binding is placement = "remote" is judged through the
// remote channel (sty_f5cae2e5): one performer node per gate, ended after the
// verdict, its payload inline because the node cannot read this machine's scratch
// paths. The channel is a fake.

const remoteGateAgent = "step-pi"

func remoteGateBinding() config.AgentBinding {
	return config.AgentBinding{
		Role: config.RoleReviewer, Command: "pi -p {system}", Tools: "read,grep",
		Placement: config.PlacementRemote, Remote: "rmt_test",
	}
}

// remoteGateEngine is an engine whose edge is gated by rev-a under the remote
// reviewer binding. The default runner and any locally built runner must not run.
func remoteGateEngine(t *testing.T, ch RemoteChannel) (*Engine, *bundleRunner) {
	t.Helper()
	wf := spineWF("", "", "", "in_progress|executor||rev-a|"+remoteGateAgent+"||", "done")
	local := &bundleRunner{}
	g := New(local, fakeDocs{workflow: wf, extraSkills: bundleSkillDocs("rev-a")}, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		if name == remoteGateAgent {
			return remoteGateBinding(), true
		}
		return config.AgentBinding{}, false
	})
	g.newRunner = func(string, string) (agentcli.Runner, error) {
		t.Error("a local runner was built for a remote-placed reviewer")
		return local, nil
	}
	if ch != nil {
		g.SetRemoteChannel(ch)
	}
	return g, local
}

func remoteGateItem() workitem.Item {
	return workitem.Item{ID: "sty_remotegate", Status: "backlog", Category: "feature", Title: "T"}
}

func TestRemoteReviewerGateIsJudgedThroughTheChannel(t *testing.T) {
	run := &fakeRemoteRun{reply: `{"decision":"accept","notes":"remote verdict"}`}
	ch := &fakeChannel{run: run}
	g, local := remoteGateEngine(t, ch)

	dec, err := g.Gate(context.Background(), remoteGateItem(), "in_progress")
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !dec.Accept {
		t.Fatalf("decision = %+v, want the remote's accept", dec)
	}
	rv := reviewerOf(t, dec, "rev-a")
	if !rv.Accept || !strings.Contains(rv.Notes, "remote verdict") {
		t.Errorf("verdict = %+v, want the remote performer's accept and notes", rv)
	}
	if ch.opens != 1 || ch.name != remoteGateAgent || ch.binding.Remote != "rmt_test" || ch.story != "sty_remotegate" {
		t.Fatalf("channel opened %d times for (%q, remote %q, %q), want once for the %s binding and the story",
			ch.opens, ch.name, ch.binding.Remote, ch.story, remoteGateAgent)
	}
	if len(run.bodies) != 1 || run.bodies[0].Binding != remoteGateAgent {
		t.Fatalf("jobs = %+v, want one job for the %s binding", run.bodies, remoteGateAgent)
	}
	if run.endings != 1 {
		t.Errorf("channel ended %d times, want once after the verdict", run.endings)
	}
	if local.calls() != 0 {
		t.Errorf("the default runner ran %d times; a remote reviewer runs nowhere local", local.calls())
	}

	// The payload is the inline work item, not a rewrite into scratch paths.
	text := run.bodies[0].Text
	if !strings.Contains(text, "sty_remotegate") || strings.Contains(text, "/material/") {
		t.Errorf("payload = %q, want the story inline and no scratch material paths", text)
	}
	if strings.Contains(run.bodies[0].SystemPrompt, "files under this dispatch's scratch directory") {
		t.Error("the system prompt tells a remote reviewer to open local scratch paths")
	}
	if !strings.Contains(run.bodies[0].SystemPrompt, "docs") || !strings.Contains(run.bodies[0].SystemPrompt, "rev-a") {
		t.Errorf("system prompt lacks the inline briefing or the rubric: %q", run.bodies[0].SystemPrompt)
	}
}

func TestRemoteReviewerGateEndsTheChannelOnAFailedRun(t *testing.T) {
	run := &fakeRemoteRun{reqErr: errors.New("the node was lost")}
	g, _ := remoteGateEngine(t, &fakeChannel{run: run})
	if dec, err := g.Gate(context.Background(), remoteGateItem(), "in_progress"); err == nil && dec.Accept {
		t.Fatalf("a lost node accepted the edge: %+v", dec)
	}
	if run.endings != 1 {
		t.Errorf("channel ended %d times, want once", run.endings)
	}
}

func TestRemoteReviewerGateWithoutAChannelRefusesNamingTheBinding(t *testing.T) {
	g, local := remoteGateEngine(t, nil)
	dec, err := g.Gate(context.Background(), remoteGateItem(), "in_progress")
	if err == nil || !strings.Contains(err.Error(), "["+remoteGateAgent+"]") {
		t.Fatalf("err = %v (decision %+v), want a refusal naming the %s binding", err, dec, remoteGateAgent)
	}
	if dec.Accept || local.calls() != 0 {
		t.Errorf("a gate with no channel accepted (%v) or fell back to a local reviewer (%d calls)", dec.Accept, local.calls())
	}
}
