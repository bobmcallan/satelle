package remotenode

import (
	"context"
	"time"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// rigSession is the id of the server-minted session pairServer connects.
const rigSession = "nod_resume"

// pairServer connects a server-minted session (sty_ee78148f): local attaches by the
// node's published key and the node joins with its node token, as satelle.dev
// starts them, so both ends redial after a relay restart. The node serves with
// report in the background; the returned handle reports how its Serve ended.
func (r *rig) pairServer(node *Node, report Report, extra ...remotechan.Option) (*remotechan.Session, *nodeHandle) {
	r.t.Helper()
	id, err := remotechan.NewIdentity()
	if err != nil {
		r.t.Fatal(err)
	}
	ctx := testCtx(r.t)
	opts := append(append([]remotechan.Option(nil), r.chanOpts...), extra...)
	type attached struct {
		s   *remotechan.Session
		err error
	}
	att := make(chan attached, 1)
	go func() {
		s, err := remotechan.AttachServerSession(ctx, r.client, r.relay.URL(), rigSession,
			func(context.Context) ([]byte, error) { return id.Public(), nil }, opts...)
		att <- attached{s, err}
	}()
	eventually(r.t, "the starter on the relay", func() bool { return r.relay.Upgrades() >= 1 })

	h := &nodeHandle{done: make(chan error, 1)}
	go func() {
		sess, err := remotechan.Join(ctx, r.relay.URL(), rigSession, id, "node-token", opts...)
		if err != nil {
			h.done <- err
			return
		}
		h.mu.Lock()
		h.sess = sess
		h.mu.Unlock()
		defer sess.Close()
		h.done <- node.Serve(ctx, sess, report)
	}()
	a := <-att
	if a.err != nil {
		r.t.Fatalf("AttachServerSession: %v", a.err)
	}
	r.t.Cleanup(func() { a.s.Close() })
	return a.s, h
}

// attachServer is pairServer with the controller's Run on top.
func (r *rig) attachServer(node *Node, report Report, extra ...remotechan.Option) (*Run, *nodeHandle) {
	r.t.Helper()
	sess, h := r.pairServer(node, report, extra...)
	run, err := Attach(testCtx(r.t), sess, 20*time.Second)
	if err != nil {
		r.t.Fatalf("Attach: %v", err)
	}
	r.t.Cleanup(func() { _ = run.End() })
	return run, h
}

// driveUntil advances the rig's clock in key-half-resend steps until cond holds:
// the redial's resends and backoffs are all on that clock.
func (r *rig) driveUntil(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out driving the clock until %s", what)
		}
		r.clock.Advance(remotechan.HelloResend)
		// Real time per step bounds the fake time a wait in real time can spend: a
		// job waiting out a gap is lost after remotechan.LostAfter of it.
		time.Sleep(25 * time.Millisecond)
	}
}

// request runs run.Request in the background and returns what it will report.
type requested struct {
	body []byte
	err  error
}

func (r *rig) request(run *Run, jobType string, body any) <-chan requested {
	out := make(chan requested, 1)
	ctx := testCtx(r.t)
	go func() {
		b, err := run.Request(ctx, jobType, body)
		out <- requested{b, err}
	}()
	return out
}

// settled reports whether res has arrived, keeping what it got.
func settled(res <-chan requested, got *requested) func() bool {
	return func() bool {
		select {
		case *got = <-res:
			return true
		default:
			return false
		}
	}
}
