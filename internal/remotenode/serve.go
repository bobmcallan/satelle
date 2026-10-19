package remotenode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/bobmcallan/satelle/internal/remotechan"
)

// Prepare readies the node from its Setup: it unpacks the substrate, indexes it,
// fetches the repos and checks the bindings, and returns the Report. It runs on
// the node, in its working root.
type Prepare func(ctx context.Context, s Setup) (Report, error)

// Handler serves one job type on the node: it receives the request body and
// returns the reply body.
type Handler func(ctx context.Context, body json.RawMessage) (json.RawMessage, error)

// Node is the node's end of a run. Register job types with Handle, then Serve.
type Node struct {
	mu       sync.Mutex
	handlers map[string]Handler
}

// NewNode returns a Node with no job types.
func NewNode() *Node { return &Node{handlers: map[string]Handler{}} }

// Handle registers the handler for a job type. A later story adds its own type
// here; an unregistered type is answered as unsupported, never ignored.
func (n *Node) Handle(jobType string, h Handler) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.handlers[jobType] = h
}

func (n *Node) handler(jobType string) (Handler, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	h, ok := n.handlers[jobType]
	return h, ok
}

// Serve runs the node's side of an admitted session until it ends: the first
// message must be the Setup, which prepare turns into the ready Report; every
// later message is a job request. Each job heartbeats while its handler runs and
// ends with its reply. It returns nil when local satelle closed the channel, and
// prepare's error (after reporting it) if the node could not become ready.
func (n *Node) Serve(ctx context.Context, sess *remotechan.Session, prepare Prepare) error {
	m, err := sess.Recv(ctx)
	if err != nil {
		return fmt.Errorf("remotenode: waiting for setup: %w", err)
	}
	env, err := decode(m.Body)
	if err != nil || env.Type != typeSetup || m.Job != "" {
		return n.refuse(ctx, sess, "the first message was not the setup")
	}
	var setup Setup
	if err := json.Unmarshal(env.Body, &setup); err != nil {
		return n.refuse(ctx, sess, "the setup is malformed")
	}
	report, perr := prepare(ctx, setup)
	if perr != nil {
		report = Report{Error: perr.Error()}
	}
	body, err := encode(typeReady, report)
	if err != nil {
		return err
	}
	if err := sess.Send(ctx, "", body); err != nil {
		return err
	}
	if perr != nil {
		return perr
	}

	jobCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	for {
		m, err := sess.Recv(ctx)
		if err != nil {
			if errors.Is(err, remotechan.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if m.Job == "" {
			continue // nothing outside a job is expected after setup
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.serveJob(jobCtx, sess, m)
		}()
	}
}

func (n *Node) refuse(ctx context.Context, sess *remotechan.Session, why string) error {
	body, _ := encode(typeReady, Report{Error: why})
	_ = sess.Send(ctx, "", body)
	return errors.New("remotenode: " + why)
}

// serveJob answers one request: heartbeat while the handler runs, send its
// reply, then announce the job done.
func (n *Node) serveJob(ctx context.Context, sess *remotechan.Session, m remotechan.Message) {
	job := sess.StartJob(m.Job)
	var r reply
	env, err := decode(m.Body)
	switch {
	case err != nil:
		r.Error = err.Error()
	default:
		h, ok := n.handler(env.Type)
		if !ok {
			r.Error = fmt.Sprintf("unsupported job type %q", env.Type)
			break
		}
		if body, herr := h(ctx, env.Body); herr != nil {
			r.Error = herr.Error()
		} else {
			r.Body = body
		}
	}
	if out, merr := encode(typeReply, r); merr == nil {
		_ = sess.Send(ctx, m.Job, out)
	}
	_ = job.Done()
}
