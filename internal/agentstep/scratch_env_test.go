package agentstep

import (
	"context"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// scratchRef is the reserved reference a binding env value may carry.
const scratchRef = "${" + config.ScratchEnv + "}"

// TestOverlayScratchEnv: the reference in binding env values becomes the
// dispatch's scratch dir, the reserved pair beats a binding-authored TMPDIR, and
// the input map is not mutated.
func TestOverlayScratchEnv(t *testing.T) {
	in := map[string]string{"FOO": scratchRef + "/bin", "TMPDIR": "/elsewhere", "PLAIN": "v"}
	got := overlayScratchEnv(in, "/scratch/d1")
	want := map[string]string{
		"FOO": "/scratch/d1/bin", "PLAIN": "v",
		"TMPDIR": "/scratch/d1", config.ScratchEnv: "/scratch/d1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("overlay = %v, want %v", got, want)
	}
	if in["FOO"] != scratchRef+"/bin" || in["TMPDIR"] != "/elsewhere" || len(in) != 3 {
		t.Errorf("input mutated: %v", in)
	}
}

// TestBindingEnvScratchRefOneShot: a one-shot dispatch substitutes the reference
// with the invocation's own scratch dir.
func TestBindingEnvScratchRefOneShot(t *testing.T) {
	r := &fakeRunner{out: "ok"}
	g := New(r, fakeDocs{workflow: testWorkflow}, "/repo", "")
	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Command: "claude", Tools: "Read", Principles: config.PrinciplesNone,
			Env: map[string]string{"FOO": scratchRef + "/bin"}},
		Section: "planner", Payload: map[string]string{}, Expect: ExpectPerform, Runner: r,
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	dir := r.got.Env[config.ScratchEnv]
	if dir == "" || r.got.Env["FOO"] != dir+"/bin" {
		t.Errorf("env FOO = %q, scratch = %q", r.got.Env["FOO"], dir)
	}
}

// TestBindingEnvScratchRefLive: a live session (orchestrator/relay) gets the same
// substitution against the session's scratch dir.
func TestBindingEnvScratchRefLive(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		return config.AgentBinding{Interface: "stream", Tools: "Read", Command: "claude -p {tools}",
			Env: map[string]string{"FOO": scratchRef + "/bin"}}, name == "orchestrator"
	})
	var got agentcli.Request
	g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
		return func(_ context.Context, req agentcli.Request, _ agentcli.PermissionPolicy) (agentcli.Session, error) {
			got = req
			return closedSess{}, nil
		}, nil
	}
	sess, err := g.OpenSessionAsWithModel(context.Background(), "orchestrator", SessionRoleDriving,
		workitem.Item{ID: "sty_live", Status: "in_progress"}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	dir := got.Env[config.ScratchEnv]
	if dir == "" || got.Env["FOO"] != dir+"/bin" {
		t.Errorf("live env FOO = %q, scratch = %q", got.Env["FOO"], dir)
	}
}
