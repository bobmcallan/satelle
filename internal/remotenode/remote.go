package remotenode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/release"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// Ledger kinds for a node's host lifecycle live in package ledger; the payload of
// each is a hostEvent.

// ErrJobLost: a job's heartbeats stopped, or the channel ended, before the node
// answered it.
var ErrJobLost = errors.New("remotenode: job lost")

// stopTimeout bounds host.stop at the end of a run.
const stopTimeout = 2 * time.Minute

// StartOptions is everything Start needs from the caller.
type StartOptions struct {
	// Name is the remote's name; it appears in the ledger and the report only.
	Name string
	Def  remotedef.Definition
	// DefDir is the remote's directory; its substrate is shipped to the node.
	DefDir string
	// Allow is the egress allowlist (remotedef.Allowlist), applied through
	// host.network and echoed back by the node.
	Allow []string
	// Client signs in to the relay; RelayURL is the relay it dials.
	Client   *hosted.Client
	RelayURL string
	// Env is the host commands' extra environment, KEY=VALUE.
	Env []string
	// ReleaseBase is SATELLE_RELEASE_BASE as local satelle sees it ("" for the
	// public releases); it is handed to the node's installer.
	ReleaseBase string
	// Secrets is satelle-server's secrets service, the one place a secret value
	// is held. Nil answers every secret request with a refusal.
	Secrets SecretService
	// Recorder is where each host start and stop is recorded. Nil records nothing.
	Recorder remotechan.Recorder
	// Log receives host command output; nil discards it.
	Log io.Writer
	// Channel configures the channel (HTTP client, clock); tests inject here.
	Channel []remotechan.Option
	// ReadyTimeout bounds the wait for the node's report once it is enrolled.
	ReadyTimeout time.Duration
	// Now is the time source for ledger entries; nil is time.Now.
	Now func() time.Time
}

const defaultReadyTimeout = 10 * time.Minute

// Run is a started node and local satelle's handle on it: the report it gave,
// the channel to it, and the held host process that keeps it awake. End it with
// End.
type Run struct {
	Report Report

	opts StartOptions
	cmds *Commands
	sess *remotechan.Session
	held *Held

	mu      sync.Mutex
	pending map[string]*pendingJob
	ended   bool
}

type result struct {
	body json.RawMessage
	err  error
}

// pendingJob is a request local satelle is waiting on. run is the server-side
// run registered for it ("" when the remote reads no secret), closed is set once
// the job has finished or been reported lost, and nonces are the secret requests
// already seen in it. All three are guarded by Run.mu.
type pendingJob struct {
	ch     chan result
	run    string
	closed bool
	nonces map[string]bool
}

// hostEvent is the payload of a remote_start, remote_stop and remote_idle entry.
type hostEvent struct {
	Remote    string `json:"remote"`
	Label     string `json:"label"`
	Lifecycle string `json:"lifecycle"`
	Error     string `json:"error,omitempty"`
}

// Start brings a node up from opts.Def and returns it ready: host.start, host.network,
// a held host.run that installs the pinned satelle and joins, the channel's
// enrollment, the substrate sent over it, and the node's report. If anything after
// host.start fails the run is ended (its lifecycle applied) before Start returns
// the error. A report that names a different satelle version than the definition
// pins, or an error of the node's own, fails the start.
func Start(ctx context.Context, opts StartOptions) (*Run, error) {
	r := &Run{opts: opts, pending: map[string]*pendingJob{}}
	r.cmds = &Commands{Def: opts.Def, Env: opts.Env, Log: opts.Log}
	if r.opts.ReadyTimeout <= 0 {
		r.opts.ReadyTimeout = defaultReadyTimeout
	}

	substrate, err := PackSubstrate(opts.DefDir)
	if err != nil {
		return nil, err
	}

	if err := r.cmds.Start(ctx); err != nil {
		r.record(ctx, ledger.KindRemoteStart, err)
		return nil, err
	}
	r.record(ctx, ledger.KindRemoteStart, nil)

	if err := r.begin(ctx, substrate); err != nil {
		if endErr := r.End(ctx); endErr != nil {
			err = errors.Join(err, endErr)
		}
		return nil, err
	}
	return r, nil
}

// begin is Start after host.start: everything that can fail with the host up.
func (r *Run) begin(ctx context.Context, substrate []byte) error {
	opts := r.opts
	if err := r.cmds.Network(ctx, opts.Allow); err != nil {
		return err
	}

	launch := remotechan.LauncherFunc(func(lctx context.Context, spec remotechan.LaunchSpec) ([]byte, error) {
		script := Bootstrap{
			Version:     opts.Def.SatelleVersion,
			InstallURL:  release.InstallScriptURL(release.Repo, opts.ReleaseBase, opts.Def.SatelleVersion),
			ReleaseBase: opts.ReleaseBase,
			Steps:       opts.Def.Host.Bootstrap,
			Spec:        spec,
		}.Script()
		held, err := r.cmds.Hold(lctx, script)
		if err != nil {
			return nil, err
		}
		r.held = held
		return held.NodeKey(lctx)
	})
	sess, err := remotechan.Open(ctx, opts.Client, opts.RelayURL, launch, opts.Channel...)
	if err != nil {
		return fmt.Errorf("remote did not enroll: %w", err)
	}
	r.sess = sess

	setup, err := encode(typeSetup, Setup{Substrate: substrate, Allow: opts.Allow, Repos: repoSpecs(opts.Def.Repos)})
	if err != nil {
		return err
	}
	if err := sess.Send(ctx, "", setup); err != nil {
		return err
	}

	rctx, cancel := context.WithTimeout(ctx, opts.ReadyTimeout)
	defer cancel()
	m, err := sess.Recv(rctx)
	if err != nil {
		return fmt.Errorf("the node did not report ready: %w", err)
	}
	env, err := decode(m.Body)
	if err != nil || env.Type != typeReady {
		return errors.New("the node's first message was not its ready report")
	}
	if err := json.Unmarshal(env.Body, &r.Report); err != nil {
		return fmt.Errorf("the node's report is malformed: %w", err)
	}
	if r.Report.Error != "" {
		return fmt.Errorf("the node is not ready: %s", r.Report.Error)
	}
	if got, want := strings.TrimPrefix(r.Report.Version, "v"), strings.TrimPrefix(opts.Def.SatelleVersion, "v"); got != want {
		return fmt.Errorf("the node runs satelle %s but remote %q pins %s", r.Report.Version, opts.Name, opts.Def.SatelleVersion)
	}

	go r.pump()
	go r.watchLost()
	return nil
}

func repoSpecs(in []remotedef.Repo) []RepoSpec {
	out := make([]RepoSpec, len(in))
	for i, r := range in {
		out[i] = RepoSpec{URL: r.URL, Ref: r.Ref}
	}
	return out
}

// Request sends the node one job of jobType carrying body and waits for its
// reply. The job heartbeats on the channel while the node works; if its
// heartbeats stop or the channel ends first, Request returns an error wrapping
// ErrJobLost. A job type is any name the node registered a handler for.
//
// When the definition lists secrets and a SecretService is configured, the job
// has a run on the server: registered before the job is sent (a failure to
// register fails the request), refreshed while the job is live and ended however
// Request returns. The node's secret requests are answered only inside that run.
func (r *Run) Request(ctx context.Context, jobType string, body any) (json.RawMessage, error) {
	payload, err := encode(jobType, body)
	if err != nil {
		return nil, err
	}
	id, err := newJobID()
	if err != nil {
		return nil, err
	}
	pj := &pendingJob{ch: make(chan result, 1), nonces: map[string]bool{}}
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: the run has ended", ErrJobLost)
	}
	r.pending[id] = pj
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		pj.closed = true
		delete(r.pending, id)
		r.mu.Unlock()
	}()

	// A remote that may read secrets has its run registered before the job is
	// sent and ended when the job finishes or is lost, by whatever way Request
	// returns.
	if r.readsSecrets() {
		end, err := r.beginSecretRun(ctx, id, pj)
		if err != nil {
			return nil, err
		}
		defer end()
	}

	r.sess.Expect(id)
	if err := r.sess.Send(ctx, id, payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrJobLost, err)
	}
	select {
	case res := <-pj.ch:
		return res.body, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Done is closed when the channel to the node has ended.
func (r *Run) Done() <-chan struct{} { return r.sess.Done() }

// HostDone is closed when the held host.run process has exited.
func (r *Run) HostDone() <-chan struct{} {
	if r.held == nil {
		return nil
	}
	return r.held.Done()
}

// KillHost kills the held host.run process and its whole group, without ending
// the run: the node's host loses its last session, as it would if the exec
// session were cut.
func (r *Run) KillHost() {
	if r.held != nil {
		r.held.Kill()
	}
}

// pump routes each reply to the request waiting on its job, and fails every
// waiting request when the channel ends.
func (r *Run) pump() {
	for {
		m, err := r.sess.Recv(context.Background())
		if err != nil {
			r.failAll(fmt.Errorf("%w: the channel ended: %v", ErrJobLost, err))
			return
		}
		if m.Job == "" {
			continue
		}
		var res result
		env, derr := decode(m.Body)
		if derr == nil && env.Type == typeSecretGet {
			go r.answerSecret(m.Job, env)
			continue
		}
		var rep reply
		switch {
		case derr != nil:
			res.err = derr
		case env.Type != typeReply:
			res.err = fmt.Errorf("remotenode: unexpected %q message in job %s", env.Type, m.Job)
		case json.Unmarshal(env.Body, &rep) != nil:
			res.err = errors.New("remotenode: malformed reply")
		case rep.Error != "":
			res.err = errors.New(rep.Error)
		default:
			res.body = rep.Body
		}
		r.deliver(m.Job, res)
	}
}

// watchLost turns the channel's lost-job reports into failed requests.
func (r *Run) watchLost() {
	for {
		select {
		case l := <-r.sess.Lost():
			r.deliver(l.Job, result{err: fmt.Errorf("%w: no heartbeat since %s", ErrJobLost, l.LastBeat.UTC().Format(time.RFC3339))})
		case <-r.sess.Done():
			return
		}
	}
}

// deliver ends the pending job with res: a reply, or the report that it is lost.
// The job is closed first, so no secret is answered for it afterwards.
func (r *Run) deliver(job string, res result) {
	r.mu.Lock()
	pj := r.pending[job]
	if pj != nil {
		pj.closed = true
	}
	r.mu.Unlock()
	if pj == nil {
		return // a finished job's late heartbeat or done
	}
	select {
	case pj.ch <- res:
	default:
	}
}

func (r *Run) failAll(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, pj := range r.pending {
		pj.closed = true
		select {
		case pj.ch <- result{err: err}:
		default:
		}
	}
}

// End finishes the run: it closes the channel, kills the held host.run process
// and its group, and then applies the definition's lifecycle — host.stop for
// "shutdown", nothing for "idle" — recording the stop (or the idle) in the
// ledger. It is safe to call more than once; only the first does anything.
func (r *Run) End(ctx context.Context) error {
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return nil
	}
	r.ended = true
	r.mu.Unlock()

	if r.sess != nil {
		_ = r.sess.Close()
	}
	r.KillHost()

	// The caller's ctx may be the very thing that ended the run (an interrupt);
	// stopping the host must still happen.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	if r.opts.Def.EffectiveLifecycle() == remotedef.LifecycleIdle {
		r.record(sctx, ledger.KindRemoteIdle, nil)
		return nil
	}
	err := r.cmds.Stop(sctx)
	r.record(sctx, ledger.KindRemoteStop, err)
	return err
}

// record writes one host lifecycle entry; a failure to record is not a failure of
// the run.
func (r *Run) record(ctx context.Context, kind string, cause error) {
	if r.opts.Recorder == nil {
		return
	}
	ev := hostEvent{Remote: r.opts.Name, Label: r.opts.Def.Identity.Label, Lifecycle: r.opts.Def.EffectiveLifecycle()}
	if cause != nil {
		ev.Error = cause.Error()
	}
	payload, _ := json.Marshal(ev)
	now := time.Now
	if r.opts.Now != nil {
		now = r.opts.Now
	}
	_, _ = r.opts.Recorder.Append(ctx, ledger.AppendInput{
		Kind: kind, Actor: "remotenode", Body: r.opts.Name, Payload: payload,
	}, now())
}

func newJobID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "job-" + hex.EncodeToString(b), nil
}
