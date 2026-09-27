package agentstep

import (
	"context"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestOpenSessionAsWithModelCarriesDispatchMarkers pins AC4 (sty_719c4a7b): a
// live session satelle itself opens — the rework relay's coder
// (SessionRoleDriving) AND its consultant (SessionRoleConsult) alike — is a
// dispatch, not the in-loop driver, even though it inherits the driver's
// SATELLE_SESSION stamp. Without SATELLE_DISPATCH_* set on its own process
// env, the live session's own hooks would see the inherited session id, take
// isDispatchedProcess() as false, and publish role in-loop over the driver's
// own file — exactly the bug TestBindSessionIDSkipsInLoopPublishWhenDispatched
// (internal/cli) proves the marker prevents, once the marker is actually set.
func TestOpenSessionAsWithModelCarriesDispatchMarkers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding string
		role    SessionRole
	}{
		{"coder", "coder", SessionRoleDriving},
		{"consultant", "reviewer-consult", SessionRoleConsult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.SessionEnv, "driver-sess")
			g, _ := newEngine(t, "", fakeDocs{})
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				return config.AgentBinding{Interface: "stream", Tools: "Read", Command: "claude -p {tools}"}, name == tc.binding
			})
			var got agentcli.Request
			g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
				return func(_ context.Context, req agentcli.Request, _ agentcli.PermissionPolicy) (agentcli.Session, error) {
					got = req
					return closedSess{}, nil
				}, nil
			}
			sess, err := g.OpenSessionAsWithModel(context.Background(), tc.binding, tc.role,
				workitem.Item{ID: "sty_live", Status: "in_progress"}, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()

			want := map[string]string{
				config.DispatchAgentEnv: tc.binding,
				config.DispatchStepEnv:  "in_progress",
				config.DispatchItemEnv:  "sty_live",
				config.SessionEnv:       "driver-sess",
			}
			for k, v := range want {
				if got.Env[k] != v {
					t.Errorf("Env[%q] = %q, want %q; full env=%v", k, got.Env[k], v, got.Env)
				}
			}
		})
	}
}
