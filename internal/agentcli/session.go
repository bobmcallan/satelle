package agentcli

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Session is a live agent session that can take more than one user turn
// (sty_d244fe1b). One-shot dispatch is open → Send → drain Events → Close.
// Events() is a fan-out over the existing OnEvent path; the channel is buffered
// and drops oldest on overflow so a caller that never reads cannot deadlock.
type Session interface {
	Send(ctx context.Context, turn Turn) error
	Events() <-chan Event
	Cancel() error
	Close() error
	Captured() []byte
}

// SessionOpener opens a live session. Request already carries system, tools,
// model, effort, dir, env, sink, capture, and OnEvent.
type SessionOpener func(ctx context.Context, req Request, pol PermissionPolicy) (Session, error)

// Turn is one user message. The first turn may fold System + Text; later turns
// carry Text only.
type Turn struct {
	System string
	Text   string
}

// PermissionRequest is a tool-use ask from a live session.
type PermissionRequest struct {
	ToolName string
	Kind     string
}

// PermissionDecision is allow/deny from PermissionPolicy.
type PermissionDecision struct {
	Allow bool
}

// PermissionPolicy decides a tool-use ask. Both ACP and stream inject the same
// default (mutator kinds denied unless the grant allows mutators).
type PermissionPolicy func(PermissionRequest) PermissionDecision

// IsMutatorRequest reports whether a permission ask is a tree-mutating or
// arbitrary-code tool. Shared by ACP, stream, and the chat permission gate
// (sty_1de7494c) so the mutator classification is not forked.
func IsMutatorRequest(req PermissionRequest) bool {
	kind := req.Kind
	if kind == "" {
		kind = toolNameKind(req.ToolName)
	}
	return isMutatorToolKind(kind)
}

func defaultPermissionPolicy(allowMutators bool) PermissionPolicy {
	return func(req PermissionRequest) PermissionDecision {
		kind := req.Kind
		if kind == "" {
			kind = toolNameKind(req.ToolName)
		}
		deny := isMutatorToolKind(kind) && !allowMutators
		return PermissionDecision{Allow: !deny}
	}
}

// toolNameKind maps a Claude/stream tool name onto an ACP ToolKind for the
// shared isMutatorToolKind ceiling — adapter, not a second policy.
func toolNameKind(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit", "write", "notebookedit", "multiedit", "strreplace", "search_replace", "write_file":
		return "edit"
	case "bash", "shell", "run_terminal", "run_terminal_cmd":
		return "execute"
	default:
		return name
	}
}

func runOneShot(ctx context.Context, sess Session, req Request) ([]byte, error) {
	if err := sess.Send(ctx, Turn{System: req.SystemPrompt, Text: req.Payload}); err != nil {
		closeErr := sess.Close()
		out := sess.Captured()
		if closeErr != nil {
			return out, closeErr
		}
		return out, err
	}
	for {
		select {
		case <-ctx.Done():
			_ = sess.Cancel()
			_ = sess.Close()
			return sess.Captured(), ctx.Err()
		case ev, ok := <-sess.Events():
			if !ok {
				out := sess.Captured()
				err := sess.Close()
				return out, err
			}
			if ev.Kind == EventFailed {
				closeErr := sess.Close()
				if closeErr != nil {
					return sess.Captured(), closeErr
				}
				if ev.Error != "" {
					return sess.Captured(), fmt.Errorf("%s", ev.Error)
				}
				return sess.Captured(), fmt.Errorf("agentcli: session failed")
			}
			if ev.Kind == EventCompleted {
				out := sess.Captured()
				err := sess.Close()
				return out, err
			}
		}
	}
}

const eventChanCap = 64

// fanoutEvents wraps parent (typically req.OnEvent / eventStream) and returns a
// buffered Events() channel plus a closer. The channel is NOT closed on
// EventCompleted — a live session may complete a turn and still take another.
// Close the channel on EventFailed or via the returned closer (EOF / Close).
func fanoutEvents(parent EventHandler) (EventHandler, <-chan Event, func()) {
	ch := make(chan Event, eventChanCap)
	var mu sync.Mutex
	closed := false
	closer := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			close(ch)
		}
	}
	h := func(ev Event) {
		if parent != nil {
			parent(ev)
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		select {
		case ch <- ev:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
		if ev.Kind == EventFailed {
			closed = true
			close(ch)
		}
	}
	return h, ch, closer
}
