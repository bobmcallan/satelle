package remotenode

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// Attach sends the node its Setup over the enrolled channel — substrate, allowlist
// and repos intact — and returns the report the node gave.
func TestAttachSendsTheSetupAndReturnsTheReport(t *testing.T) {
	r := newRig(t)
	setup := Setup{
		Substrate: []byte("packed-substrate"),
		Allow:     []string{"git.example.com", "satelle.test"},
		Repos:     []RepoSpec{{URL: "https://git.example.com/team/tool.git", Ref: "main"}},
	}
	var gotSetup Setup
	run, _ := r.attach(echoNode(), func(_ context.Context, s Setup) (Report, error) {
		gotSetup = s
		return Report{Version: "1.2.3", Workflows: []string{"default"}, Allow: s.Allow, Bindings: []string{"worker"}}, nil
	}, setup, nil)

	if run.Report.Version != "1.2.3" || !reflect.DeepEqual(run.Report.Workflows, []string{"default"}) ||
		!reflect.DeepEqual(run.Report.Bindings, []string{"worker"}) {
		t.Errorf("report = %+v", run.Report)
	}
	if string(gotSetup.Substrate) != "packed-substrate" {
		t.Error("the node was not sent the substrate")
	}
	if !reflect.DeepEqual(gotSetup.Allow, setup.Allow) {
		t.Errorf("setup allow = %v", gotSetup.Allow)
	}
	if !reflect.DeepEqual(gotSetup.Repos, setup.Repos) {
		t.Errorf("setup repos = %v, want %v", gotSetup.Repos, setup.Repos)
	}
}

// A message sent to the node's agent comes back as the reply of a channel job, and
// the job carries a heartbeat for as long as the node works: the request is still
// alive after twice LostAfter of channel time because the node kept beating, and
// its reply arrives.
func TestAgentRequestHeartbeatsAndReplies(t *testing.T) {
	r := newRig(t)
	release := make(chan struct{})
	started := make(chan struct{})
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
		var req AgentRequest
		_ = json.Unmarshal(body, &req)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return json.Marshal(AgentReply{Text: "re: " + req.Text})
	})
	run, _ := r.attach(node, readyPrepare("1.2.3"), Setup{}, nil)

	type out struct {
		body json.RawMessage
		err  error
	}
	res := make(chan out, 1)
	before := r.remoteFrames()
	go func() {
		b, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "hello"})
		res <- out{b, err}
	}()
	<-started
	eventually(t, "the job's first heartbeat", func() bool { return r.remoteFrames() > before })
	base := r.remoteFrames()

	steps := 2 * int(remotechan.LostAfter/remotechan.HeartbeatInterval)
	for i := 1; i <= steps; i++ {
		r.clock.Advance(remotechan.HeartbeatInterval)
		want := base + i
		eventually(t, "a heartbeat for each interval", func() bool { return r.remoteFrames() >= want })
		select {
		case o := <-res:
			t.Fatalf("request ended early at step %d: %v %v", i, o.body, o.err)
		default:
		}
	}
	close(release)
	o := <-res
	if o.err != nil {
		t.Fatalf("Request: %v", o.err)
	}
	rep, err := DecodeAgentReply(o.body)
	if err != nil || rep.Text != "re: hello" {
		t.Errorf("reply = %+v, %v", rep, err)
	}
}

// The channel carries any job type a later story registers, and a type the node has
// no handler for is answered as unsupported.
func TestNamedJobTypes(t *testing.T) {
	r := newRig(t)
	node := echoNode()
	node.Handle("custom.sum", func(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
		var xs []int
		if err := json.Unmarshal(body, &xs); err != nil {
			return nil, err
		}
		n := 0
		for _, x := range xs {
			n += x
		}
		return json.Marshal(n)
	})
	run, _ := r.attach(node, readyPrepare("1.2.3"), Setup{}, nil)

	body, err := run.Request(testCtx(t), "custom.sum", []int{1, 2, 3})
	if err != nil || string(body) != "6" {
		t.Fatalf("custom.sum = %s, %v", body, err)
	}
	_, err = run.Request(testCtx(t), "no.such.type", nil)
	if err == nil || !strings.Contains(err.Error(), `unsupported job type "no.such.type"`) {
		t.Errorf("an unregistered type must be answered unsupported, got %v", err)
	}
}

// A node whose host freezes — its channel time stops while the connection stays up
// — has its job reported lost within LostAfter + HeartbeatInterval of its last
// heartbeat, measured on the injected fake clock.
func TestAFrozenNodeReportsTheJobLost(t *testing.T) {
	r := newRig(t)
	nodeClock := &freezable{inner: r.clock}
	started := make(chan struct{})
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done() // the agent works until the channel goes
		return nil, ctx.Err()
	})
	run, _ := r.attach(node, readyPrepare("1.2.3"), Setup{}, nodeClock)

	res := make(chan error, 1)
	before := r.remoteFrames()
	go func() {
		_, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "work"})
		res <- err
	}()
	<-started
	eventually(t, "the job's first heartbeat", func() bool { return r.remoteFrames() > before })

	// Healthy for a while: the heartbeats keep it alive.
	for i := 1; i <= 2; i++ {
		base := r.remoteFrames()
		r.clock.Advance(remotechan.HeartbeatInterval)
		eventually(t, "a heartbeat", func() bool { return r.remoteFrames() > base })
	}
	select {
	case err := <-res:
		t.Fatalf("job ended while healthy: %v", err)
	default:
	}

	lastBeat := r.clock.Now()
	nodeClock.freeze()

	var lostErr error
	for r.clock.Now().Sub(lastBeat) <= remotechan.LostAfter+remotechan.HeartbeatInterval+remotechan.HeartbeatInterval && lostErr == nil {
		r.clock.Advance(remotechan.HeartbeatInterval)
		select {
		case lostErr = <-res:
		case <-time.After(300 * time.Millisecond):
		}
	}
	if !errors.Is(lostErr, ErrJobLost) {
		t.Fatalf("the job was not reported lost, got %v", lostErr)
	}
	silent := r.clock.Now().Sub(lastBeat)
	if silent > remotechan.LostAfter+remotechan.HeartbeatInterval {
		t.Errorf("reported lost after %v of silence, want within %v", silent, remotechan.LostAfter+remotechan.HeartbeatInterval)
	}
	if silent <= remotechan.LostAfter {
		t.Errorf("reported lost after only %v, before LostAfter %v", silent, remotechan.LostAfter)
	}
}

// A node whose channel ends mid-job fails the request rather than leaving it
// waiting.
func TestChannelEndFailsTheRequest(t *testing.T) {
	r := newRig(t)
	started := make(chan struct{})
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	run, h := r.attach(node, readyPrepare("1.2.3"), Setup{}, nil)
	res := make(chan error, 1)
	go func() {
		_, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "x"})
		res <- err
	}()
	<-started
	h.dropChannel()
	select {
	case err := <-res:
		if !errors.Is(err, ErrJobLost) {
			t.Errorf("err = %v, want ErrJobLost", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request never failed")
	}
}

// End closes the channel; ending twice does nothing more, and a request after the
// end is a lost job rather than a hang.
func TestEndClosesTheChannelOnce(t *testing.T) {
	r := newRig(t)
	run, h := r.attach(echoNode(), readyPrepare("1.2.3"), Setup{}, nil)
	if err := run.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	if err := run.End(); err != nil {
		t.Errorf("second End: %v", err)
	}
	if _, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "late"}); !errors.Is(err, ErrJobLost) {
		t.Errorf("a request after End = %v, want ErrJobLost", err)
	}
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Error("the node kept serving after the controller closed the channel")
	}
}

// A node that cannot become ready fails the attach naming the cause, and the
// controller's end of the channel is closed rather than left behind.
func TestAttachFailsNamingTheCause(t *testing.T) {
	r := newRig(t)
	sess, _ := r.open(echoNode(), func(context.Context, Setup) (Report, error) {
		return Report{}, errors.New(`binding "worker": command "no-such-agent" is not executable on this machine`)
	}, r.clock)
	run, err := Attach(testCtx(t), sess, Setup{}, 20*time.Second)
	if err == nil {
		_ = run.End()
		t.Fatal("Attach must fail")
	}
	for _, w := range []string{`binding "worker"`, "no-such-agent"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q should name %q", err, w)
		}
	}
	select {
	case <-sess.Done():
	case <-time.After(10 * time.Second):
		t.Error("a failed attach left the channel open")
	}
}
