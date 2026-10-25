package remotenode

import (
	"context"
	"encoding/json"
	"testing"
)

// Node.Status is the node's heartbeat: running, and working only while a job's
// handler runs.
func TestNodeStatusReportsWorkingWhileAHandlerRunsAndWaitingAfter(t *testing.T) {
	r := newRig(t)
	release := make(chan struct{})
	started := make(chan struct{})
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return json.Marshal(AgentReply{Text: "done"})
	})
	if got := node.Status(); got.State != StateRunning || got.Step != StepWaiting {
		t.Errorf("status before any job = %+v, want running and waiting", got)
	}
	run, _ := r.attach(node, ready("1.2.3"), nil)

	res := make(chan error, 1)
	go func() {
		_, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "go"})
		res <- err
	}()
	<-started
	if got := node.Status(); got.State != StateRunning || got.Step != StepWorking {
		t.Errorf("status while the handler runs = %+v, want running and working", got)
	}
	close(release)
	if err := <-res; err != nil {
		t.Fatalf("Request: %v", err)
	}
	eventually(t, "the node to report waiting again", func() bool { return node.Status().Step == StepWaiting })
}
