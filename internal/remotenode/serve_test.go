package remotenode

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// sty_ee78148f: a job running on the node when the relay restarts has its context
// cancelled with ErrRelayRestarted. The controller already failed it, so the node
// sends no reply for it (even though the handler returns one), and Serve keeps
// serving over the resumed channel.
func TestServeJobCancelledOnRelayRestart(t *testing.T) {
	r := newRig(t)
	started := make(chan struct{})
	cause := make(chan error, 1)
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		cause <- context.Cause(ctx)
		return json.Marshal(AgentReply{Text: "late"}) // a reply that must not be sent
	})
	node.Handle("probe", func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.Marshal("probed")
	})
	local, h := r.pairServer(node, ready("1.2.3"))
	ctx := testCtx(t)

	if _, err := local.Recv(ctx); err != nil { // the ready report
		t.Fatalf("ready: %v", err)
	}
	cut, _ := encode(AgentJob, AgentRequest{Text: "work"})
	if err := local.Send(ctx, "cut", cut); err != nil {
		t.Fatal(err)
	}
	<-started

	r.relay.Restart()
	select {
	case err := <-cause:
		if !errors.Is(err, remotechan.ErrRelayRestarted) {
			t.Errorf("handler context cause = %v, want ErrRelayRestarted", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the running handler was not cancelled")
	}

	// Over the resumed channel the node still answers, and nothing for "cut" came first.
	probe, _ := encode("probe", nil)
	sent := make(chan error, 1)
	go func() { sent <- local.Send(ctx, "probe", probe) }()
	r.driveUntil("the probe sent", func() bool {
		select {
		case err := <-sent:
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			sent <- nil
			return true
		default:
			return false
		}
	})
	for {
		m, err := local.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if m.Job == "cut" {
			t.Fatalf("the node replied to the job the restart cut off: %s", m.Body)
		}
		if m.Job == "probe" {
			break
		}
	}
	select {
	case err := <-h.done:
		t.Fatalf("Serve stopped: %v", err)
	default:
	}
}
