package remotenode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotechan/relaytest"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// fakeClock is a remotechan.Clock a test advances, the same shape remotechan's own
// tests inject. Each Advance step delivers its ticks with a blocking send, so a
// tick is never dropped under load.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

type fakeTicker struct {
	d       time.Duration
	next    time.Time
	c       chan time.Time
	stopped bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Tick(d time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{d: d, next: f.now.Add(d), c: make(chan time.Time, 1)}
	f.tickers = append(f.tickers, t)
	return t.c, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		t.stopped = true
	}
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	target := f.now.Add(d)
	f.mu.Unlock()
	for {
		f.mu.Lock()
		var due *fakeTicker
		for _, t := range f.tickers {
			if !t.stopped && !t.next.After(target) && (due == nil || t.next.Before(due.next)) {
				due = t
			}
		}
		if due == nil {
			f.now = target
			f.mu.Unlock()
			return
		}
		f.now = due.next
		due.next = due.next.Add(due.d)
		at := f.now
		f.mu.Unlock()
		select {
		case due.c <- at:
		case <-time.After(2 * time.Second):
		}
	}
}

// freezable is a Clock whose ticks stop on demand while its time source keeps
// going: the node's clock after its host has frozen, because the last exec
// session on it ended.
type freezable struct {
	inner  remotechan.Clock
	frozen atomic.Bool
}

func (f *freezable) Now() time.Time { return f.inner.Now() }

func (f *freezable) Tick(d time.Duration) (<-chan time.Time, func()) {
	in, stop := f.inner.Tick(d)
	out := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case t := <-in:
				if !f.frozen.Load() {
					select {
					case out <- t:
					case <-done:
						return
					}
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() { stop(); close(done) }
}

func (f *freezable) freeze() { f.frozen.Store(true) }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type memStore struct{ cred hosted.Credential }

func (m *memStore) Load(string) (hosted.Credential, error) { return m.cred, nil }
func (m *memStore) Save(c hosted.Credential) error         { m.cred = c; return nil }
func (m *memStore) Delete(string) error                    { return nil }

// rig is local satelle, a stand-in relay and a fake host: host commands that are
// shell scripts, which log what they were handed and, for run, stand in for the
// node's shell by recording the bootstrap command and printing the node's key.
type rig struct {
	t        *testing.T
	relay    *relaytest.Relay
	client   *hosted.Client
	clock    *fakeClock
	ledger   *ledger.Store
	id       *remotechan.Identity
	dir      string // fake host working dir
	logPath  string
	cmdPath  string
	defDir   string
	def      remotedef.Definition
	chanOpts []remotechan.Option
	secrets  SecretService
}

const rigServer = "https://satelle.test"

func newRig(t *testing.T, lifecycle string) *rig {
	t.Helper()
	r := &rig{t: t, relay: relaytest.New(t), clock: newFakeClock()}
	r.client = hosted.NewClient(rigServer, &memStore{cred: hosted.Credential{ServerURL: rigServer, AccessToken: "access-1"}}, nil)
	r.chanOpts = []remotechan.Option{remotechan.WithHTTPClient(r.relay.Client()), remotechan.WithClock(r.clock)}

	db, err := sql.Open("sqlite", "file:remotenode-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := ledger.Migrate(db); err != nil {
		t.Fatal(err)
	}
	r.ledger = ledger.New(db)

	if r.id, err = remotechan.NewIdentity(); err != nil {
		t.Fatal(err)
	}
	r.dir = t.TempDir()
	r.logPath = filepath.Join(r.dir, "host.log")
	r.cmdPath = filepath.Join(r.dir, "bootstrap.sh")

	r.defDir = remotedef.Dir(t.TempDir(), "edge")
	if err := remotedef.Scaffold(r.defDir, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	def, err := remotedef.Load(r.defDir)
	if err != nil {
		t.Fatal(err)
	}
	def.Lifecycle = lifecycle
	def.Identity.Label = "edge-1"
	def.Repos = []remotedef.Repo{{URL: "https://git.example.com/team/tool.git", Ref: "main"}}
	def.Host.Start = []string{"sh", "-c", `echo "start $0" >> "$1"`, "{label}", r.logPath}
	def.Host.Network = []string{"sh", "-c", `echo "network $0 $1" >> "$2"`, "{label}", "{allow}", r.logPath}
	def.Host.Stop = []string{"sh", "-c", `echo "stop $0" >> "$1"`, "{label}", r.logPath}
	// run records the bootstrap command it was handed, prints the node's key the
	// way join does, and then holds like an exec session until it is killed.
	key := base64.StdEncoding.EncodeToString(r.id.Public())
	def.Host.Run = []string{"sh", "-c", `printf '%s' "$2" > "$1"; echo "run $0" >> "$3"; echo "installing…"; echo "` + KeyLinePrefix + key + `"; exec sleep 600`,
		"{label}", r.cmdPath, "{command}", r.logPath}
	r.def = def
	return r
}

func (r *rig) options() StartOptions {
	return StartOptions{
		Name: "edge", Def: r.def, DefDir: r.defDir,
		Allow:  remotedef.Allowlist(r.def, []string{"rel.example.com"}, "satelle.test"),
		Client: r.client, RelayURL: r.relay.URL(), Recorder: r.ledger, Secrets: r.secrets,
		Channel: r.chanOpts, ReadyTimeout: 20 * time.Second,
		Now: r.clock.Now,
	}
}

var joinArgs = regexp.MustCompile(`--relay '([^']*)' --session '([^']*)' --token '([^']*)'`)

// nodeHandle is the in-process node.
type nodeHandle struct {
	done chan error
	mu   sync.Mutex
	sess *remotechan.Session
}

// dropChannel closes the node's end of the channel, as a crashed node would.
func (h *nodeHandle) dropChannel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sess != nil {
		h.sess.Close()
	}
}

// runNode plays the node: once the fake host has recorded the bootstrap command
// it joins with the arguments that command carries and serves. clock is the
// node's own time source.
func (r *rig) runNode(node *Node, prepare Prepare, clock remotechan.Clock) *nodeHandle {
	h := &nodeHandle{done: make(chan error, 1)}
	done := h.done
	ctx := testCtx(r.t)
	go func() {
		var script []byte
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(r.cmdPath); err == nil && len(b) > 0 {
				script = b
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		m := joinArgs.FindSubmatch(script)
		if m == nil {
			done <- fmt.Errorf("the bootstrap command carries no join arguments:\n%s", script)
			return
		}
		sess, err := remotechan.Join(ctx, string(m[1]), string(m[2]), r.id, string(m[3]),
			remotechan.WithHTTPClient(r.relay.Client()), remotechan.WithClock(clock))
		if err != nil {
			done <- err
			return
		}
		h.mu.Lock()
		h.sess = sess
		h.mu.Unlock()
		defer sess.Close()
		done <- node.Serve(ctx, sess, prepare)
	}()
	return h
}

// readyPrepare reports ready as version, unpacking nothing.
func readyPrepare(version string) Prepare {
	return func(_ context.Context, s Setup) (Report, error) {
		return Report{Version: version, Workflows: []string{"*"}, Allow: s.Allow}, nil
	}
}

func (r *rig) hostLog() []string {
	b, _ := os.ReadFile(r.logPath)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// entries reads the ledger entries of kind.
func (r *rig) entries(kind string) []hostEvent {
	r.t.Helper()
	rows, err := r.ledger.List(context.Background(), ledger.ListFilter{Kind: kind})
	if err != nil {
		r.t.Fatal(err)
	}
	var out []hostEvent
	for _, e := range rows {
		var ev hostEvent
		if err := json.Unmarshal(e.Payload, &ev); err != nil {
			r.t.Fatalf("payload %s: %v", e.Payload, err)
		}
		out = append(out, ev)
	}
	return out
}

func echoNode() *Node {
	n := NewNode()
	n.Handle(AgentJob, func(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
		var req AgentRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return json.Marshal(AgentReply{Text: "echo: " + req.Text})
	})
	return n
}

// hostGone reports whether the held host.run process has exited.
func hostGone(run *Run) bool {
	select {
	case <-run.HostDone():
		return true
	default:
		return false
	}
}

// remoteFrames counts the frames the relay has forwarded from the node.
func (r *rig) remoteFrames() int {
	n := 0
	for _, f := range r.relay.Frames() {
		if f.From == "remote" {
			n++
		}
	}
	return n
}
