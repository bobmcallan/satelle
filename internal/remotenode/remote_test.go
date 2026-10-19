package remotenode

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// start brings the rig's node up and returns the Run. nodeClock is the node's own
// time source; nil shares the rig's clock.
func (r *rig) start(t *testing.T, node *Node, prepare Prepare, nodeClock remotechan.Clock) (*Run, *nodeHandle) {
	t.Helper()
	if nodeClock == nil {
		nodeClock = r.clock
	}
	h := r.runNode(node, prepare, nodeClock)
	run, err := Start(testCtx(t), r.options())
	if err != nil {
		t.Fatalf("Start: %v\nhost log: %v", err, r.hostLog())
	}
	t.Cleanup(func() { _ = run.End(context.Background()) })
	return run, h
}

// AC4/AC6: the host commands run in order with the definition's own argv, the
// node enrolls on the key the host command printed, receives the setup and
// reports ready, and Start returns the report.
func TestStartRunsHostCommandsAndReturnsTheReport(t *testing.T) {
	r := newRig(t, "")
	var gotSetup Setup
	run, _ := r.start(t, echoNode(), func(_ context.Context, s Setup) (Report, error) {
		gotSetup = s
		return Report{Version: "1.2.3", Workflows: []string{"default"}, Allow: s.Allow, Bindings: []string{"worker"}}, nil
	}, nil)

	if run.Report.Version != "1.2.3" || !reflect.DeepEqual(run.Report.Workflows, []string{"default"}) {
		t.Errorf("report = %+v", run.Report)
	}

	allow := strings.Join(r.options().Allow, ",")
	wantLog := []string{"start edge-1", "network edge-1 " + allow, "run edge-1"}
	if got := r.hostLog(); !reflect.DeepEqual(got, wantLog) {
		t.Errorf("host commands =\n%v\nwant\n%v", got, wantLog)
	}

	packed, err := PackSubstrate(r.defDir)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSetup.Substrate) != string(packed) {
		t.Error("the node was not sent the remote's substrate")
	}
	if !reflect.DeepEqual(gotSetup.Allow, r.options().Allow) {
		t.Errorf("setup allow = %v", gotSetup.Allow)
	}
	if want := []RepoSpec{{URL: "https://git.example.com/team/tool.git", Ref: "main"}}; !reflect.DeepEqual(gotSetup.Repos, want) {
		t.Errorf("setup repos = %v, want %v", gotSetup.Repos, want)
	}
}

// AC7: {allow} reaches host.network as the comma-joined union of the release
// hosts the caller passed, the server host, each repo host and [network] allow.
func TestHostNetworkReceivesTheAllowlist(t *testing.T) {
	r := newRig(t, "")
	r.def.Network.Allow = []string{"extra.example.net"}
	r.start(t, echoNode(), readyPrepare("1.2.3"), nil)
	var network string
	for _, l := range r.hostLog() {
		if rest, ok := strings.CutPrefix(l, "network edge-1 "); ok {
			network = rest
		}
	}
	want := "*.satelle.test,extra.example.net,git.example.com,rel.example.com,satelle.test"
	if network != want {
		t.Errorf("host.network got {allow} = %q, want %q", network, want)
	}
}

// AC8: a message sent to the node's agent comes back as the reply of a channel
// job, and the job carries a heartbeat for as long as the node works: the request
// is still alive after twice LostAfter of channel time because the node kept
// beating, and its reply arrives.
func TestAgentRequestHeartbeatsAndReplies(t *testing.T) {
	r := newRig(t, "")
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
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)

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

// AC8: the channel carries any job type a later story registers, and a type the
// node has no handler for is answered as unsupported.
func TestNamedJobTypes(t *testing.T) {
	r := newRig(t, "")
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
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)

	body, err := run.Request(testCtx(t), "custom.sum", []int{1, 2, 3})
	if err != nil || string(body) != "6" {
		t.Fatalf("custom.sum = %s, %v", body, err)
	}
	_, err = run.Request(testCtx(t), "no.such.type", nil)
	if err == nil || !strings.Contains(err.Error(), `unsupported job type "no.such.type"`) {
		t.Errorf("an unregistered type must be answered unsupported, got %v", err)
	}
}

// AC8: kill the held host.run process and the node's host freezes — its channel
// time stops while the connection stays up. The job is reported lost within
// LostAfter + HeartbeatInterval of its last heartbeat, measured on the injected
// fake clock.
func TestKillingTheHeldHostProcessReportsTheJobLost(t *testing.T) {
	r := newRig(t, "")
	nodeClock := &freezable{inner: r.clock}
	started := make(chan struct{})
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done() // the agent works until the channel goes
		return nil, ctx.Err()
	})
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nodeClock)
	go func() { <-run.HostDone(); nodeClock.freeze() }()

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
	run.KillHost()
	if !hostGone(run) {
		t.Fatal("KillHost left the held process running")
	}
	eventually(t, "the node's host to freeze", func() bool { return nodeClock.frozen.Load() })

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
	r := newRig(t, "")
	started := make(chan struct{})
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	run, h := r.start(t, node, readyPrepare("1.2.3"), nil)
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

// AC9: shutdown applies host.stop, idle does not; the held process is gone
// either way, and each start and stop (or idle) is recorded.
func TestLifecycles(t *testing.T) {
	for _, tc := range []struct {
		lifecycle                                 string
		wantStop                                  bool
		wantStartRows, wantStopRows, wantIdleRows int
	}{
		{"", true, 1, 1, 0},
		{remotedef.LifecycleShutdown, true, 1, 1, 0},
		{remotedef.LifecycleIdle, false, 1, 0, 1},
	} {
		t.Run("lifecycle="+tc.lifecycle, func(t *testing.T) {
			r := newRig(t, tc.lifecycle)
			run, _ := r.start(t, echoNode(), readyPrepare("1.2.3"), nil)
			if hostGone(run) {
				t.Fatal("the held host.run process must stay up for the length of the run")
			}
			if err := run.End(context.Background()); err != nil {
				t.Fatalf("End: %v", err)
			}
			if !hostGone(run) {
				t.Error("End must kill the held host.run process")
			}
			stopped := false
			for _, l := range r.hostLog() {
				if l == "stop edge-1" {
					stopped = true
				}
			}
			if stopped != tc.wantStop {
				t.Errorf("host.stop ran = %v, want %v (log %v)", stopped, tc.wantStop, r.hostLog())
			}
			if got := len(r.entries(ledger.KindRemoteStart)); got != tc.wantStartRows {
				t.Errorf("remote_start rows = %d, want %d", got, tc.wantStartRows)
			}
			if got := len(r.entries(ledger.KindRemoteStop)); got != tc.wantStopRows {
				t.Errorf("remote_stop rows = %d, want %d", got, tc.wantStopRows)
			}
			idle := r.entries(ledger.KindRemoteIdle)
			if len(idle) != tc.wantIdleRows {
				t.Errorf("remote_idle rows = %d, want %d", len(idle), tc.wantIdleRows)
			}
			for _, ev := range append(r.entries(ledger.KindRemoteStart), idle...) {
				if ev.Remote != "edge" || ev.Label != "edge-1" || ev.Error != "" {
					t.Errorf("ledger payload = %+v", ev)
				}
			}
			// Ending twice does nothing more.
			if err := run.End(context.Background()); err != nil {
				t.Errorf("second End: %v", err)
			}
			if got := len(r.entries(ledger.KindRemoteStop)) + len(r.entries(ledger.KindRemoteIdle)); got != tc.wantStopRows+tc.wantIdleRows {
				t.Errorf("a second End recorded again: %d rows", got)
			}
		})
	}
}

// AC6: a node that cannot resolve a binding, and a node on a different satelle
// than the definition pins, each fail the start naming the cause — and the run
// is ended under its lifecycle, not left behind.
func TestStartFailsNamingTheCause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare Prepare
		want    []string
	}{
		{"unresolved binding", func(context.Context, Setup) (Report, error) {
			return Report{}, errors.New(`binding "worker": command "no-such-agent" is not executable on this machine`)
		}, []string{`binding "worker"`, "no-such-agent"}},
		{"wrong version", readyPrepare("1.2.4"), []string{"1.2.4", "1.2.3", `"edge"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, "")
			r.runNode(echoNode(), tc.prepare, r.clock)
			run, err := Start(testCtx(t), r.options())
			if err == nil {
				_ = run.End(context.Background())
				t.Fatal("Start must fail")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q should name %q", err, w)
				}
			}
			if log := r.hostLog(); log[len(log)-1] != "stop edge-1" {
				t.Errorf("a failed start must end the run under its lifecycle; host log %v", log)
			}
			if len(r.entries(ledger.KindRemoteStop)) != 1 {
				t.Error("the stop was not recorded")
			}
		})
	}
}

// A host.start that fails stops the start before anything else runs, and the
// failure is recorded.
func TestHostStartFailure(t *testing.T) {
	r := newRig(t, "")
	r.def.Host.Start = []string{"sh", "-c", "exit 3"}
	if _, err := Start(testCtx(t), r.options()); err == nil || !strings.Contains(err.Error(), "host.start") {
		t.Fatalf("err = %v, want it to name host.start", err)
	}
	if log := r.hostLog(); len(log) != 0 {
		t.Errorf("nothing may run after a failed host.start, got %v", log)
	}
	rows := r.entries(ledger.KindRemoteStart)
	if len(rows) != 1 || rows[0].Error == "" {
		t.Errorf("the failed start must be recorded with its error, got %+v", rows)
	}
}

// A host.run that ends before printing the node's key is a failed launch, not a
// hang.
func TestHostRunEndingWithoutAKey(t *testing.T) {
	r := newRig(t, "")
	r.def.Host.Run = []string{"sh", "-c", "echo oops >&2; exit 1"}
	_, err := Start(testCtx(t), r.options())
	if err == nil || !strings.Contains(err.Error(), "before the node printed its key") {
		t.Fatalf("err = %v", err)
	}
}
