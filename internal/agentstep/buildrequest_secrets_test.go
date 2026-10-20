package agentstep

import (
	"context"
	"slices"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// A remote run's secrets reach the step performer's request only; the identity
// variable is dropped from every request buildRequest makes (sty_0b1c72a9).
func TestBuildRequestHandsRunSecretsToThePerformerOnly(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{workflow: testWorkflow}, "/repo", "")
	g.SetRunSecrets(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok-123"}, []string{"SATELLE_REMOTE_KEY"})

	for _, tc := range []struct {
		name      string
		inv       invocation
		wantValue bool
	}{
		{"performer", invocation{rubric: "R", payload: map[string]string{}, performer: true}, true},
		{"reviewer", invocation{rubric: "R", payload: map[string]string{}, judging: true}, false},
		{"summariser", invocation{rubric: "R", payload: map[string]string{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := g.buildRequest(context.Background(), tc.inv)
			if err != nil {
				t.Fatal(err)
			}
			got, has := req.Env["CLAUDE_CODE_OAUTH_TOKEN"]
			if has != tc.wantValue || (has && got != "tok-123") {
				t.Errorf("secret in Env = %q (present %v), want present %v", got, has, tc.wantValue)
			}
			if !slices.Contains(req.Unset, "SATELLE_REMOTE_KEY") {
				t.Errorf("the identity variable must be unset on every request: %v", req.Unset)
			}
		})
	}
}

// The binding's own env is not changed by a performer request: a secret is added
// to a copy.
func TestBuildRequestDoesNotWriteSecretsIntoTheBindingEnv(t *testing.T) {
	g := New(&fakeRunner{}, fakeDocs{workflow: testWorkflow}, "/repo", "")
	g.SetRunSecrets(map[string]string{"K": "v"}, nil)
	shared := map[string]string{"A": "1"}
	if _, err := g.buildRequest(context.Background(), invocation{rubric: "R", payload: 1, performer: true, env: shared}); err != nil {
		t.Fatal(err)
	}
	if _, leaked := shared["K"]; leaked || len(shared) != 1 {
		t.Errorf("the shared env map was written: %v", shared)
	}
}

// Through Invoke: an ExpectPerform dispatch is the performer and an ExpectVerdict
// dispatch is not.
func TestInvokeMarksOnlyPerformDispatchesAsPerformers(t *testing.T) {
	for _, tc := range []struct {
		expect Expect
		out    string
		want   bool
	}{
		{ExpectPerform, "done", true},
		{ExpectVerdict, `{"decision":"accept","notes":"ok"}`, false},
	} {
		r := &fakeRunner{out: tc.out}
		g := New(r, fakeDocs{workflow: testWorkflow}, "/repo", "")
		g.SetRunSecrets(map[string]string{"K": "v"}, []string{"ID_VAR"})
		binding := config.AgentBinding{Command: "claude", Tools: "Read", Model: "sonnet", Principles: config.PrinciplesNone}
		res := g.Invoke(context.Background(), InvokeRequest{Binding: binding, Section: "agent", Rubric: "r", Payload: map[string]string{"x": "1"}, Expect: tc.expect, Runner: r, Skill: "s"})
		if res.Err != nil {
			t.Fatalf("expect %v: %v", tc.expect, res.Err)
		}
		if _, has := r.got.Env["K"]; has != tc.want {
			t.Errorf("expect %v: secret in Env = %v, want %v", tc.expect, has, tc.want)
		}
		if !slices.Contains(r.got.Unset, "ID_VAR") {
			t.Errorf("expect %v: Unset = %v", tc.expect, r.got.Unset)
		}
	}
}
