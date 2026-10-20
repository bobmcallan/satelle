package remotenode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotechan"
)

// SecretService is satelle-server's side of the secrets contract (see package
// hosted's client_secrets.go), which *hosted.Client implements. The server holds
// every value; local satelle only passes one through, for a live run.
type SecretService interface {
	// RegisterRun tells the server a run of remote is live and which key is the
	// waker-only one, and returns the run id.
	RegisterRun(ctx context.Context, remote, wakeKey string) (string, error)
	RefreshRun(ctx context.Context, remote, run string) error
	EndRun(ctx context.Context, remote, run string) error
	// ReadSecret returns key's value for run, or a *hosted.SecretRefusedError.
	ReadSecret(ctx context.Context, remote, run, key, nonce string) (string, error)
}

const (
	// RunRefreshInterval is how often local satelle refreshes a live run: three
	// heartbeat intervals, well inside the server's backstop of about a minute.
	// The run is only as live as the job's heartbeats say it is; this is not a
	// second liveness clock, it is how often the one verdict is told to the server.
	RunRefreshInterval = 3 * remotechan.HeartbeatInterval
	// secretCallTimeout bounds one call to the server.
	secretCallTimeout = 30 * time.Second
)

// secretEvent is the payload of a secret.read ledger entry. It never holds a value.
type secretEvent struct {
	Key     string `json:"key"`
	Remote  string `json:"remote"`
	Run     string `json:"run"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// readsSecrets reports whether this remote may read any secret: it names keys in
// its definition and a service is there to answer.
func (r *Run) readsSecrets() bool {
	return r.opts.Secrets != nil && len(r.opts.Def.Secrets) > 0
}

// beginSecretRun registers the run for job id with the server, keeps it live for
// as long as the job is not reported lost, and returns the func that ends it.
// Liveness has one owner, the channel: its heartbeats decide when the job is
// lost, and this only passes that on to the server.
func (r *Run) beginSecretRun(ctx context.Context, id string, pj *pendingJob) (func(), error) {
	svc, name := r.opts.Secrets, r.opts.Name
	rctx, cancel := context.WithTimeout(ctx, secretCallTimeout)
	run, err := svc.RegisterRun(rctx, name, r.opts.Def.WakeKey)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("remotenode: registering the run for %s: %w", id, err)
	}
	r.mu.Lock()
	pj.run = run
	r.mu.Unlock()

	kctx, stop := context.WithCancel(context.WithoutCancel(ctx))
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		tick, stopTick := r.sess.Clock().Tick(RunRefreshInterval)
		defer stopTick()
		for {
			select {
			case <-kctx.Done():
				return
			case <-r.sess.Done():
				return
			case <-tick:
				r.mu.Lock()
				closed := pj.closed
				r.mu.Unlock()
				if closed {
					return
				}
				c, cancel := context.WithTimeout(kctx, secretCallTimeout)
				_ = svc.RefreshRun(c, name, run) // a run the server dropped refuses its reads
				cancel()
			}
		}
	}()
	return func() {
		stop()
		<-exited
		// The caller's ctx may be what ended the job; the run must still end.
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretCallTimeout)
		defer cancel()
		_ = svc.EndRun(c, name, run)
	}, nil
}

// answerSecret answers one secret request from the node's agent, inside job. It
// refuses, without calling the server, a key outside the definition and the
// definition's wake key; it refuses a request for a job that is not live (done or
// lost) and a nonce it has seen. Whatever the outcome it records a secret.read
// entry, without the value, and sends the node a sealed answer.
func (r *Run) answerSecret(job string, env envelope) {
	var req secretGet
	_ = json.Unmarshal(env.Body, &req)

	r.mu.Lock()
	pj := r.pending[job]
	var run string
	var live, fresh bool
	if pj != nil {
		run, live = pj.run, !pj.closed
		fresh = req.Nonce != "" && !pj.nonces[req.Nonce]
		if fresh {
			pj.nonces[req.Nonce] = true
		}
	}
	r.mu.Unlock()

	value, reason := "", ""
	switch {
	case !live:
		reason = "the job is not live"
	case !fresh:
		reason = "the request was replayed"
	case r.opts.Secrets == nil:
		reason = "no secret service is configured"
	case req.Key == "":
		reason = "no key was named"
	case req.Key == r.opts.Def.WakeKey:
		reason = "the wake key is never readable by an agent"
	case !slices.Contains(r.opts.Def.Secrets, req.Key):
		reason = "the key is not in remote " + r.opts.Name + "'s definition"
	case run == "":
		reason = "the job has no registered run"
	default:
		ctx, cancel := context.WithTimeout(context.Background(), secretCallTimeout)
		v, err := r.opts.Secrets.ReadSecret(ctx, r.opts.Name, run, req.Key, req.Nonce)
		cancel()
		var refused *hosted.SecretRefusedError
		switch {
		case errors.As(err, &refused):
			reason = refused.Reason
		case err != nil:
			reason = "the server could not answer: " + err.Error()
		default:
			value = v
		}
		// A job reported lost while the server answered gets no value.
		r.mu.Lock()
		closed := pj.closed
		r.mu.Unlock()
		if reason == "" && closed {
			value, reason = "", "the job is not live"
		}
	}

	r.recordSecret(secretEvent{Key: req.Key, Remote: r.opts.Name, Run: run, Outcome: outcome(reason), Reason: reason})
	typ, ans := typeSecretValue, secretAnswer{Key: req.Key, Nonce: req.Nonce, Value: value}
	if reason != "" {
		typ, ans = typeSecretDenied, secretAnswer{Key: req.Key, Nonce: req.Nonce, Reason: reason}
	}
	if body, err := encode(typ, ans); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), secretCallTimeout)
		defer cancel()
		_ = r.sess.Send(ctx, job, body)
	}
}

func outcome(reason string) string {
	if reason == "" {
		return "allowed"
	}
	return "denied"
}

// recordSecret writes one secret.read entry; a failure to record is not a failure
// of the run.
func (r *Run) recordSecret(ev secretEvent) {
	if r.opts.Recorder == nil {
		return
	}
	payload, _ := json.Marshal(ev)
	now := time.Now
	if r.opts.Now != nil {
		now = r.opts.Now
	}
	_, _ = r.opts.Recorder.Append(context.Background(), ledger.AppendInput{
		Kind: ledger.KindSecretRead, Actor: "remotenode", Body: ev.Key, Payload: payload,
	}, now())
}
