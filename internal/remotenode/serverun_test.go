package remotenode

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// OnReady fires once, after the ready report has been sent, and not at all for a
// report that says the node is not ready.
func TestOnReadyFiresOnceAfterTheReadyReportAndNotForAnErrorReport(t *testing.T) {
	r := newRig(t)
	var fired atomic.Int32
	node := echoNode()
	node.OnReady(func() { fired.Add(1) })
	r.attach(node, ready("1.2.3"), nil)
	eventually(t, "the ready hook", func() bool { return fired.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	if n := fired.Load(); n != 1 {
		t.Errorf("OnReady fired %d times, want once", n)
	}

	var failed atomic.Int32
	bad := echoNode()
	bad.OnReady(func() { failed.Add(1) })
	sess, h := newRig(t).open(bad, Report{Error: "no task"}, nil)
	if _, err := Attach(testCtx(t), sess, 20*time.Second); err == nil {
		t.Fatal("Attach of a node that is not ready succeeded")
	}
	<-h.done
	if n := failed.Load(); n != 0 {
		t.Errorf("OnReady fired %d times for a report with an error, want none", n)
	}
}

func serveWithRun(r *rig, run func(ctx context.Context)) {
	r.serve = func(ctx context.Context, node *Node, sess *remotechan.Session, report Report) error {
		return node.ServeWithRun(ctx, sess, report, run)
	}
}

// A run that returns at once still leaves the controller attached with the ready
// report, every time, and Serve then ends of its own accord, the node's single
// run done.
func TestServeWithRunAnInstantRunStillDeliversTheReadyReport(t *testing.T) {
	for i := 0; i < 25; i++ {
		r := newRig(t)
		serveWithRun(r, func(context.Context) {})
		_, h := r.attach(NewNode(), ready("1.2.3"), nil)
		select {
		case err := <-h.done:
			if err != nil {
				t.Fatalf("round %d: Serve ended with %v, want nil", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: the node did not end with its run", i)
		}
	}
}

// A run that takes a while does not delay the ready report, and the node does not
// end until the run has.
func TestServeWithRunReadyIsNotDelayedByTheRunAndTheNodeEndsWithIt(t *testing.T) {
	r := newRig(t)
	release := make(chan struct{})
	serveWithRun(r, func(ctx context.Context) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	_, h := r.attach(NewNode(), ready("1.2.3"), nil) // attached while the run is held
	select {
	case err := <-h.done:
		t.Fatalf("the node ended (%v) before its run did", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Serve ended with %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the node did not end after its run")
	}
}

// The run is only allowed to end the serving after the report is out. If Serve
// fails before it can send it (here the session is already closed) the hook never
// fires, and ServeWithRun must still return rather than wait on it.
func TestServeWithRunReturnsWhenServeFailsBeforeReady(t *testing.T) {
	r := newRig(t)
	var ran atomic.Bool
	r.serve = func(ctx context.Context, node *Node, sess *remotechan.Session, report Report) error {
		sess.Close()
		return node.ServeWithRun(ctx, sess, report, func(context.Context) { ran.Store(true) })
	}
	sess, h := r.open(NewNode(), ready("1.2.3"), nil)
	defer sess.Close()
	select {
	case err := <-h.done:
		if err == nil {
			t.Error("Serve over a closed session returned nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeWithRun hung waiting for a ready report that was never sent")
	}
	if !ran.Load() {
		t.Error("the run was never started")
	}
}

// If Serve ends first (the controller ended the session) the run's context is
// cancelled and ServeWithRun waits for the run to return.
func TestServeWithRunCancelsTheRunWhenServeEndsFirst(t *testing.T) {
	r := newRig(t)
	started := make(chan struct{})
	var cancelled atomic.Bool
	serveWithRun(r, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		cancelled.Store(true)
	})
	run, h := r.attach(NewNode(), ready("1.2.3"), nil)
	<-started
	if err := run.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Serve ended with %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeWithRun did not return after the session ended")
	}
	if !cancelled.Load() {
		t.Error("the run was not cancelled when Serve ended first, or ServeWithRun returned before the run did")
	}
}
