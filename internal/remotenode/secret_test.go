package remotenode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/hosted/secretstest"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotechan"
)

const (
	planted  = "planted-secret-value-7f3a"
	wakeName = "MY_WAKER"
)

// secretRig is the rig with a remote that lists two secret keys and a
// non-default wake key, a contract-mirroring fake server, and a signed-in client
// to it. API_TOKEN is uploaded and granted to the remote; OTHER is listed in the
// definition but never granted.
type secretRig struct {
	*rig
	srv *secretstest.Server
	svc *hosted.Client
}

func newSecretRig(t *testing.T) *secretRig {
	t.Helper()
	r := newRig(t, "")
	srv := secretstest.New(t)
	svc := hosted.NewClient(srv.URL(), &memStore{cred: hosted.Credential{ServerURL: srv.URL(), AccessToken: "owner"}}, nil)
	r.def.Secrets = []string{"API_TOKEN", "OTHER"}
	r.def.WakeKey = wakeName
	r.secrets = svc
	ctx := context.Background()
	for _, k := range []string{"API_TOKEN", "OTHER", wakeName} {
		if err := svc.PutSecret(ctx, k, planted+"-"+k, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.GrantSecret(ctx, "API_TOKEN", "edge", false); err != nil {
		t.Fatal(err)
	}
	return &secretRig{rig: r, srv: srv, svc: svc}
}

// socket serves the node's secret socket on a short path (unix socket paths are
// short) and returns it.
func (r *secretRig) socket(t *testing.T, node *Node) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := SocketPath(dir)
	closeFn, err := node.ServeSocket(testCtx(t), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeFn)
	return path
}

// reads is the count of read calls the fake server has received.
func (r *secretRig) reads() int {
	n := 0
	for _, q := range r.srv.Requests() {
		if strings.HasPrefix(q.Path, "/api/v1/remotes/") && strings.HasSuffix(q.Path, ":read") {
			n++
		}
	}
	return n
}

// secretEntries are the secret.read ledger rows.
func (r *secretRig) secretEntries() []secretEvent {
	r.t.Helper()
	rows, err := r.ledger.List(context.Background(), ledger.ListFilter{Kind: ledger.KindSecretRead})
	if err != nil {
		r.t.Fatal(err)
	}
	var out []secretEvent
	for _, e := range rows {
		var ev secretEvent
		if err := json.Unmarshal(e.Payload, &ev); err != nil {
			r.t.Fatalf("payload %s: %v", e.Payload, err)
		}
		out = append(out, ev)
	}
	return out
}

// noValueAnywhere fails if the planted value appears in a ledger row or in any
// frame the relay forwarded (it carries sealed bytes only).
func (r *secretRig) noValueAnywhere() {
	r.t.Helper()
	for _, kind := range []string{ledger.KindSecretRead, ledger.KindRemoteStart, ledger.KindRemoteStop, ledger.KindRemoteIdle, ledger.KindRemoteEnrollRefused} {
		rows, err := r.ledger.List(context.Background(), ledger.ListFilter{Kind: kind})
		if err != nil {
			r.t.Fatal(err)
		}
		for _, e := range rows {
			b, _ := json.Marshal(e)
			if strings.Contains(string(b), planted) {
				r.t.Errorf("ledger %s row carries a secret value: %s", e.Kind, b)
			}
		}
	}
	for _, f := range r.relay.Frames() {
		if bytes.Contains(f.Data, []byte(planted)) {
			r.t.Error("a relayed frame carries the secret value in the clear")
		}
	}
}

// askNode is a node whose agent job runs steps, replying with the text it returns.
func askNode(steps func(ctx context.Context, node *Node) string) *Node {
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(AgentReply{Text: steps(ctx, node)})
	})
	return node
}

func (r *secretRig) ask(t *testing.T, run *Run) string {
	t.Helper()
	body, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "go"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	rep, err := DecodeAgentReply(body)
	if err != nil {
		t.Fatal(err)
	}
	return rep.Text
}

// AC3: the agent's shell asks for a listed and granted key over the node's
// socket and gets the value, carried over the sealed session and fetched by local
// satelle from the server for the run local satelle registered. Nothing is in the
// clear on the relay, and the ledger records the read without the value.
func TestAgentReadsAGrantedSecretOverTheSocket(t *testing.T) {
	r := newSecretRig(t)
	var sock string
	node := askNode(func(ctx context.Context, _ *Node) string {
		v, err := SocketGet(ctx, sock, "API_TOKEN", "")
		if err != nil {
			return "error: " + err.Error()
		}
		return v
	})
	sock = r.socket(t, node)
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)

	if got := r.ask(t, run); got != planted+"-API_TOKEN" {
		t.Fatalf("the agent got %q", got)
	}
	if a := r.srv.Audit(); len(a) != 1 || a[0].Outcome != "allowed" || a[0].Key != "API_TOKEN" || a[0].Remote != "edge" || a[0].Run != "run-1" {
		t.Errorf("server audit = %+v", a)
	}
	want := secretEvent{Key: "API_TOKEN", Remote: "edge", Run: "run-1", Outcome: "allowed"}
	if es := r.secretEntries(); len(es) != 1 || es[0] != want {
		t.Errorf("secret.read entries = %+v, want [%+v]", es, want)
	}
	r.noValueAnywhere()
}

// AC5/AC9: a key outside the remote's definition and the definition's own wake
// key are refused by local satelle with no call to the server; a listed key the
// remote holds no grant on is refused by the server and its reason is surfaced.
// Each is recorded as a denied secret.read without a value.
func TestRefusedKeysAreRecordedAndTheServerIsNotAskedForLocalRefusals(t *testing.T) {
	r := newSecretRig(t)
	var sock string
	node := askNode(func(ctx context.Context, _ *Node) string {
		var out []string
		for _, k := range []string{"NOT_LISTED", wakeName, "OTHER"} {
			if v, err := SocketGet(ctx, sock, k, ""); err == nil {
				out = append(out, "GOT "+k+"="+v)
			} else {
				out = append(out, err.Error())
			}
		}
		return strings.Join(out, "\n")
	})
	sock = r.socket(t, node)
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)

	got := r.ask(t, run)
	lines := strings.Split(got, "\n")
	for i, want := range []string{"not in remote edge's definition", "wake key", "holds no grant on OTHER"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("refusal %d = %q, want it to say %q", i, lines[i], want)
		}
	}
	if strings.Contains(got, planted) {
		t.Errorf("a refusal carries a value: %s", got)
	}
	if n := r.reads(); n != 1 {
		t.Errorf("the server saw %d reads, want 1 (only the listed key)", n)
	}
	denied := map[string]string{}
	for _, e := range r.secretEntries() {
		if e.Outcome != "denied" || e.Remote != "edge" || e.Run != "run-1" {
			t.Errorf("entry = %+v", e)
		}
		denied[e.Key] = e.Reason
	}
	if len(denied) != 3 || !strings.Contains(denied["OTHER"], "holds no grant") {
		t.Errorf("denied entries = %v", denied)
	}
	r.noValueAnywhere()
}

// AC6: a key that reads while granted is refused after the grant is revoked.
func TestRevokingAGrantStopsFurtherReads(t *testing.T) {
	r := newSecretRig(t)
	var sock string
	firstRead, revoked := make(chan struct{}), make(chan struct{})
	node := askNode(func(ctx context.Context, _ *Node) string {
		first, err := SocketGet(ctx, sock, "API_TOKEN", "")
		if err != nil {
			return "first read failed: " + err.Error()
		}
		close(firstRead)
		<-revoked
		_, err = SocketGet(ctx, sock, "API_TOKEN", "")
		if err == nil {
			return "second read succeeded"
		}
		return first + "|" + err.Error()
	})
	sock = r.socket(t, node)
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)

	res := make(chan string, 1)
	go func() { res <- r.ask(t, run) }()
	<-firstRead
	if err := r.svc.RevokeSecret(context.Background(), "API_TOKEN", "edge"); err != nil {
		t.Fatal(err)
	}
	close(revoked)
	got := <-res
	if want := planted + "-API_TOKEN|"; !strings.HasPrefix(got, want) || !strings.Contains(got, "holds no grant") {
		t.Errorf("result = %q, want the first value then a no-grant refusal", got)
	}
}

// AC7: the run is registered when the job is requested, refreshed on the channel's
// clock while the job is not lost, and ended when it finishes.
func TestRunIsRegisteredRefreshedAndEndedWithTheJob(t *testing.T) {
	r := newSecretRig(t)
	started, release := make(chan struct{}), make(chan struct{})
	node := askNode(func(ctx context.Context, _ *Node) string {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done"
	})
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)

	if len(r.srv.Runs()) != 0 {
		t.Fatal("a run was registered before any job was requested")
	}
	res := make(chan string, 1)
	before := r.remoteFrames()
	go func() { res <- r.ask(t, run) }()
	<-started
	eventually(t, "the job's first heartbeat", func() bool { return r.remoteFrames() > before })
	runs := r.srv.Runs()
	if len(runs) != 1 || !r.srv.Live(runs[0]) {
		t.Fatalf("runs = %v, live = %v", runs, len(runs) == 1 && r.srv.Live(runs[0]))
	}
	id := runs[0]
	if q := r.srv.Requests(); !strings.Contains(q[len(q)-1].Body, `"wake_key":"`+wakeName+`"`) && !hasBody(r.srv, `{"wake_key":"`+wakeName+`"}`) {
		t.Error("the run was registered without the definition's wake key")
	}

	// Healthy for two refresh intervals of channel time, one heartbeat at a time.
	for elapsed := time.Duration(0); elapsed < 2*RunRefreshInterval; elapsed += remotechan.HeartbeatInterval {
		base := r.remoteFrames()
		r.clock.Advance(remotechan.HeartbeatInterval)
		eventually(t, "a heartbeat", func() bool { return r.remoteFrames() > base })
	}
	eventually(t, "two refreshes", func() bool { return r.srv.Refreshes(id) >= 2 })

	close(release)
	if got := <-res; got != "done" {
		t.Fatalf("reply = %q", got)
	}
	eventually(t, "the run to end with the job", func() bool { return !r.srv.Live(id) })
	refreshed := r.srv.Refreshes(id)
	r.clock.Advance(2 * RunRefreshInterval)
	time.Sleep(50 * time.Millisecond)
	if r.srv.Refreshes(id) != refreshed {
		t.Error("the run was refreshed after the job finished")
	}
}

func hasBody(s *secretstest.Server, body string) bool {
	for _, q := range s.Requests() {
		if q.Body == body && strings.HasSuffix(q.Path, "/runs") {
			return true
		}
	}
	return false
}

// AC7: a job reported lost ends its run and is refreshed no more; a request the
// node makes after that is refused without calling the server, and recorded.
func TestALostJobEndsItsRunAndRefusesLaterRequests(t *testing.T) {
	r := newSecretRig(t)
	nodeClock := &freezable{inner: r.clock}
	started, late := make(chan struct{}), make(chan struct{})
	lateErr := make(chan error, 1)
	node := NewNode()
	node.Handle(AgentJob, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-late
		_, err := node.FetchSecret(ctx, JobID(ctx), "API_TOKEN")
		lateErr <- err
		return nil, ctx.Err()
	})
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nodeClock)

	res := make(chan error, 1)
	before := r.remoteFrames()
	go func() {
		_, err := run.Request(testCtx(t), AgentJob, AgentRequest{Text: "work"})
		res <- err
	}()
	<-started
	eventually(t, "the job's first heartbeat", func() bool { return r.remoteFrames() > before })
	id := r.srv.Runs()[0]

	nodeClock.freeze() // the node's heartbeats stop; the connection stays up
	var lostErr error
	for i := 0; i < 20 && lostErr == nil; i++ {
		r.clock.Advance(remotechan.HeartbeatInterval)
		select {
		case lostErr = <-res:
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !errors.Is(lostErr, ErrJobLost) {
		t.Fatalf("the job was not reported lost: %v", lostErr)
	}
	eventually(t, "the run to end when the job is lost", func() bool { return !r.srv.Live(id) })
	refreshed, readsBefore := r.srv.Refreshes(id), r.reads()
	r.clock.Advance(2 * RunRefreshInterval)

	close(late)
	var err error
	select {
	case err = <-lateErr:
	case <-time.After(10 * time.Second):
		t.Fatal("the late request was never answered")
	}
	var denied *SecretDeniedError
	if !errors.As(err, &denied) || !strings.Contains(denied.Reason, "not live") {
		t.Errorf("a request after Lost = %v, want a not-live refusal", err)
	}
	if r.reads() != readsBefore || r.srv.Refreshes(id) != refreshed {
		t.Error("the server was called for a lost job")
	}
	es := r.secretEntries()
	if len(es) != 1 || es[0].Outcome != "denied" || es[0].Key != "API_TOKEN" {
		t.Errorf("entries = %+v", es)
	}
}

// AC7: a request whose nonce local satelle has already seen in the job is
// refused and never reaches the server again.
func TestAReplayedRequestIsRefused(t *testing.T) {
	r := newSecretRig(t)
	answered := make(chan struct{})
	node := askNode(func(ctx context.Context, n *Node) string {
		body, _ := encode(typeSecretGet, secretGet{Key: "API_TOKEN", Nonce: "fixed-nonce"})
		for i := 0; i < 2; i++ {
			if err := n.sess.Send(ctx, JobID(ctx), body); err != nil {
				return err.Error()
			}
		}
		<-answered // the job stays live until local satelle has handled both
		return "sent"
	})
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)
	res := make(chan string, 1)
	go func() { res <- r.ask(t, run) }()
	eventually(t, "both requests to be recorded", func() bool { return len(r.secretEntries()) == 2 })
	close(answered)
	if got := <-res; got != "sent" {
		t.Fatal(got)
	}
	var allowed, replayed int
	for _, e := range r.secretEntries() {
		switch {
		case e.Outcome == "allowed":
			allowed++
		case strings.Contains(e.Reason, "replayed"):
			replayed++
		}
	}
	if allowed != 1 || replayed != 1 || r.reads() != 1 {
		t.Errorf("allowed=%d replayed=%d server reads=%d, want 1, 1, 1", allowed, replayed, r.reads())
	}
}

// A node accepts one answer per request: a second answer for the same nonce, an
// answer in another job, or one for another key is dropped.
func TestNodeAcceptsOneAnswerPerRequest(t *testing.T) {
	n := NewNode()
	add := func() *secretWaiter {
		w := &secretWaiter{job: "j1", key: "K", ch: make(chan secretAnswer, 1)}
		n.waiters["n1"] = w
		return w
	}
	answer := func(job, key, nonce string) bool {
		body, _ := json.Marshal(secretAnswer{Key: key, Nonce: nonce, Value: "v"})
		return n.deliverSecret(job, envelope{Type: typeSecretValue, Body: body})
	}
	w := add()
	if answer("j2", "K", "n1") || answer("j1", "OTHER", "n1") || answer("j1", "K", "unknown") {
		t.Fatal("an answer that does not match the request was accepted")
	}
	if !answer("j1", "K", "n1") {
		t.Fatal("the matching answer was refused")
	}
	if got := (<-w.ch).Value; got != "v" {
		t.Errorf("value = %q", got)
	}
	if answer("j1", "K", "n1") {
		t.Error("a replayed answer was accepted")
	}
}

// A request on a node not serving that job is refused before anything is sent.
func TestFetchSecretNeedsALiveJob(t *testing.T) {
	if _, err := NewNode().FetchSecret(context.Background(), "j", "K"); err == nil {
		t.Error("a node with no session answered")
	}
}

// Without a configured service every request is refused and recorded.
func TestNoSecretServiceRefusesEveryRequest(t *testing.T) {
	r := newSecretRig(t)
	r.secrets = nil
	var sock string
	node := askNode(func(ctx context.Context, _ *Node) string {
		_, err := SocketGet(ctx, sock, "API_TOKEN", "")
		if err == nil {
			return "read"
		}
		return err.Error()
	})
	sock = r.socket(t, node)
	run, _ := r.start(t, node, readyPrepare("1.2.3"), nil)
	if got := r.ask(t, run); !strings.Contains(got, "no secret service") {
		t.Errorf("got %q", got)
	}
	if r.srv.Calls() != 4 { // only the seeding calls from newSecretRig
		t.Errorf("the server saw %d calls, want only the 3 seeding puts and 1 grant", r.srv.Calls())
	}
}

// A data dir too deep for a unix socket path gets a short, stable, private one in
// the temp dir, which a node and its shell compute alike and the node can serve.
func TestSocketPathForADeepDataDir(t *testing.T) {
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", 90), ".satelle")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	p := SocketPath(deep)
	if len(p) > maxSocketPath || !strings.HasPrefix(p, os.TempDir()) || p != SocketPath(deep) {
		t.Fatalf("SocketPath = %q (%d bytes)", p, len(p))
	}
	if p == SocketPath(filepath.Join(deep, "other")) {
		t.Error("two data dirs share a socket")
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(p)) })

	node := NewNode()
	closeFn, err := node.ServeSocket(testCtx(t), p)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if d, _ := os.Stat(filepath.Dir(p)); d.Mode().Perm() != 0o700 {
		t.Errorf("socket dir mode = %v", d.Mode().Perm())
	}
	if _, err := SocketGet(context.Background(), p, "K", ""); err == nil || !strings.Contains(err.Error(), "no job is running") {
		t.Errorf("a request over the temp-dir socket = %v", err)
	}
}

// The socket is private to the node's user.
func TestSocketIsPrivate(t *testing.T) {
	r := newSecretRig(t)
	node := NewNode()
	path := r.socket(t, node)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v", st.Mode().Perm())
	}
	if d, _ := os.Stat(filepath.Dir(path)); d.Mode().Perm() != 0o700 {
		t.Errorf("socket dir mode = %v", d.Mode().Perm())
	}
	if _, err := SocketGet(context.Background(), path, "K", ""); err == nil || !strings.Contains(err.Error(), "no job is running") {
		t.Errorf("a request with no job running = %v", err)
	}
	if _, err := SocketGet(context.Background(), filepath.Join(t.TempDir(), "none.sock"), "K", ""); err == nil || !strings.Contains(err.Error(), "remote node") {
		t.Errorf("a missing socket = %v", err)
	}
}
