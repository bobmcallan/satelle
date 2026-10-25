package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotenode"
)

// A step whose binding declares placement = "remote" (sty_7028d996) is performed
// by an ephemeral remote performer paired with this session over the remote
// channel. Local satelle starts nothing: the channel's real implementation asks
// satelle.dev to start the node and pairs with it. The performer is the node's
// own binding of the same name, run by the node; this side supplies the binding's
// name, the step's composed system prompt and its payload, and gets the
// performer's text back.
//
// The dispatch is the ordinary one with its transport swapped: the runner below
// stands where a command or ACP runner would, so the output contract, attempts,
// findings and evidence all read the remote's text exactly as they read a local
// performer's.

// RemoteChannel opens the paired channel to a remote performer of a binding for
// a story. The real implementation lives with the CLI wiring; tests install a
// fake.
type RemoteChannel interface {
	// Open starts and pairs a performer node of binding (named bindingName in
	// agents.toml, which the node also defines) for storyID.
	Open(ctx context.Context, bindingName string, binding config.AgentBinding, storyID string) (RemoteRun, error)
}

// RemoteRun is one open channel to a remote performer: the method set of
// *remotenode.Run.
type RemoteRun interface {
	Request(ctx context.Context, jobType string, body any) (json.RawMessage, error)
	End() error
}

// SetRemoteChannel wires how a placement = "remote" binding reaches its
// performer. Nil refuses such a dispatch, naming the binding: there is no local
// fallback.
func (g *Engine) SetRemoteChannel(c RemoteChannel) { g.remoteChannel = c }

// remoteRunner is the agentcli.Runner of a remote-placed binding. It opens the
// channel on its first Run and holds it until Close, so a step's attempts share
// one performer.
type remoteRunner struct {
	channel RemoteChannel
	name    string
	binding config.AgentBinding
	storyID string

	mu  sync.Mutex
	run RemoteRun
}

func (r *remoteRunner) Name() string { return "remote" }

// localPathsUnreadable marks the performer as unable to read this machine's
// scratch files, so a judging payload is sent inline (see referencedWorkItem).
func (r *remoteRunner) localPathsUnreadable() {}

func (r *remoteRunner) Command() string { return "remote:" + r.binding.Remote + "/" + r.name }

func (r *remoteRunner) Run(ctx context.Context, req agentcli.Request) ([]byte, error) {
	run, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	body, err := run.Request(ctx, remotenode.AgentJob, remotenode.AgentRequest{
		Binding: r.name, SystemPrompt: req.SystemPrompt, Text: req.Payload,
	})
	if err != nil {
		return nil, fmt.Errorf("remote performer %s: %w", r.Command(), err)
	}
	reply, err := remotenode.DecodeAgentReply(body)
	if err != nil {
		return nil, fmt.Errorf("remote performer %s: its reply cannot be read: %w", r.Command(), err)
	}
	return []byte(reply.Text), nil
}

func (r *remoteRunner) open(ctx context.Context) (RemoteRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run != nil {
		return r.run, nil
	}
	run, err := r.channel.Open(ctx, r.name, r.binding, r.storyID)
	if err != nil {
		return nil, fmt.Errorf("remote performer %s: %w", r.Command(), err)
	}
	r.run = run
	return run, nil
}

// Close ends the channel if it was opened.
func (r *remoteRunner) Close() error {
	r.mu.Lock()
	run := r.run
	r.run = nil
	r.mu.Unlock()
	if run == nil {
		return nil
	}
	return run.End()
}

// remoteRunnerFor is the runner for a remote-placed binding, or an error naming
// the binding when no channel is wired.
func (g *Engine) remoteRunnerFor(name string, binding config.AgentBinding, storyID string) (*remoteRunner, error) {
	if g.remoteChannel == nil {
		return nil, errors.New("binding [" + name + "] is placement = \"remote\" but no remote channel is available (is this session signed in to satelle.dev?)")
	}
	return &remoteRunner{channel: g.remoteChannel, name: name, binding: binding, storyID: storyID}, nil
}
