// Package remotechan is the sealed channel between local satelle and an agent on
// a remote it started. Both ends dial OUT to a relay (wss on 443) and neither
// listens, so a corporate network needs no extra rule. The relay pairs the two
// ends by session id and forwards bytes; it cannot read them.
//
// # Relay contract (what the satelle-server relay must do)
//
// A relay serves GET <relayURL>/<session>?role=local|remote as a WebSocket
// upgrade. The local end sends "Authorization: Bearer <satelle.dev access
// token>"; the relay answers 401 for a bearer it does not accept, and the local
// end then refreshes through hosted.Client.WithBearer and retries once. The
// remote end sends no bearer: it holds only the session id and a single-use
// enrollment token, and proves itself to the local end, not to the relay. The
// local end dials first, then launches the node; a remote that arrives with no
// local end waiting is refused (404). Every binary message from one end is
// forwarded unchanged to the other; the relay never needs to parse one.
//
// # Wire protocol
//
// Binary messages only. remote → local: [1][remote ephemeral X25519 public
// key]; local → remote: [2][local ephemeral public key]; then both ways
// [3][seq, 8 bytes][AES-256-GCM ciphertext]. The keys come from HKDF-SHA256
// over the ephemeral–ephemeral DH and the local ephemeral–node-static DH, the
// latter against the key local satelle pinned at launch (seal.go). Everything
// after the two key halves is sealed, including the enrollment token and the
// heartbeats; the relay and any proxy see only types, lengths and sequence
// numbers.
//
// Enrollment: the remote's first sealed message is {t:enroll, token}. Local
// satelle opens it (a failure means the remote does not hold the pinned key:
// ErrKeyMismatch), redeems the token (single use, EnrollTTL), and answers
// {t:admit} or {t:refuse}. Nothing the remote sends before it is admitted is
// delivered to the caller.
package remotechan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/coder/websocket"
)

// maxFrameBytes bounds one relay message in either direction.
const maxFrameBytes = 1 << 20

// Role is which end of the channel a Session is.
type Role byte

const (
	// RoleLocal is local satelle, the end that launched the node.
	RoleLocal Role = iota + 1
	// RoleRemote is the agent on the remote.
	RoleRemote
)

func (r Role) String() string {
	if r == RoleRemote {
		return "remote"
	}
	return "local"
}

// The application messages carried inside sealed frames.
const (
	msgEnroll    = "enroll"
	msgAdmit     = "admit"
	msgRefuse    = "refuse"
	msgData      = "msg"
	msgHeartbeat = "hb"
	msgDone      = "done"
)

// appMsg is what a sealed frame carries. Only the relay's view of it — a
// ciphertext — leaves the process.
type appMsg struct {
	T      string `json:"t"`
	Job    string `json:"job,omitempty"`
	Body   []byte `json:"body,omitempty"`
	Token  string `json:"token,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Message is an application message exchanged over a Session. Job names the job
// it belongs to, or is empty.
type Message struct {
	Job  string
	Body []byte
}

// ErrClosed is returned by Send and Recv once the Session has ended.
var ErrClosed = errors.New("remotechan: session closed")

// Session is an admitted, sealed channel to the other end. It is safe for
// concurrent use.
type Session struct {
	role  Role
	conn  *websocket.Conn
	clock Clock

	ctx    context.Context
	cancel context.CancelFunc

	wmu  sync.Mutex // serialises seal + write so sequence numbers reach the relay in order
	send *sealer
	recv *opener

	inbox chan Message
	jobs  *jobTable
	lost  chan JobLost

	done    chan struct{}
	errOnce sync.Once
	err     error
}

// newSession wraps an admitted connection and starts its loops. The session
// outlives the ctx of the call that opened it (a per-call timeout must not end
// it); Close ends it.
func newSession(ctx context.Context, role Role, conn *websocket.Conn, send *sealer, recv *opener, clock Clock) *Session {
	conn.SetReadLimit(maxFrameBytes)
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &Session{
		role: role, conn: conn, clock: clock, ctx: ctx, cancel: cancel,
		send: send, recv: recv,
		inbox: make(chan Message, 64), jobs: newJobTable(), lost: make(chan JobLost, 16),
		done: make(chan struct{}),
	}
	go s.readLoop()
	go s.monitor()
	return s
}

// Role is which end this is.
func (s *Session) Role() Role { return s.role }

// Send delivers body to the other end, sealed. job names the job it belongs to
// ("" for none).
func (s *Session) Send(ctx context.Context, job string, body []byte) error {
	return s.sendApp(ctx, appMsg{T: msgData, Job: job, Body: body})
}

// Recv returns the next message from the other end.
func (s *Session) Recv(ctx context.Context) (Message, error) {
	select {
	case m := <-s.inbox:
		return m, nil
	case <-s.done:
		// Drain what arrived before the end.
		select {
		case m := <-s.inbox:
			return m, nil
		default:
		}
		return Message{}, s.Err()
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

// Expect starts waiting on job's heartbeats: from now, a job silent for longer
// than LostAfter is reported on Lost. Call it when the job is requested, so a
// job that never sends its first heartbeat is caught too.
func (s *Session) Expect(job string) { s.jobs.beat(job, s.clock.Now()) }

// Lost delivers each job whose heartbeats stopped, once, even while the
// connection stays up.
func (s *Session) Lost() <-chan JobLost { return s.lost }

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is why the session ended (ErrClosed after Close), or nil while it is up.
func (s *Session) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// Close ends the session.
func (s *Session) Close() error {
	s.finish(ErrClosed)
	return s.conn.Close(websocket.StatusNormalClosure, "")
}

// finish records why the session ended, once, and wakes everything waiting on it.
func (s *Session) finish(err error) {
	s.errOnce.Do(func() {
		s.err = err
		close(s.done)
		s.cancel()
	})
}

// sendApp seals one application message and writes it.
func (s *Session) sendApp(ctx context.Context, m appMsg) error {
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	select {
	case <-s.done:
		return ErrClosed
	default:
	}
	return s.conn.Write(ctx, websocket.MessageBinary, s.send.seal(plain))
}

func (s *Session) readLoop() {
	for {
		_, frame, err := s.conn.Read(s.ctx)
		if err != nil {
			s.finish(fmt.Errorf("%w: %v", ErrClosed, err))
			return
		}
		plain, err := s.recv.open(frame)
		if err != nil {
			s.finish(err)
			s.conn.Close(websocket.StatusPolicyViolation, "bad frame")
			return
		}
		var m appMsg
		if json.Unmarshal(plain, &m) != nil {
			s.finish(fmt.Errorf("%w: malformed message", ErrFrame))
			s.conn.Close(websocket.StatusPolicyViolation, "bad message")
			return
		}
		switch m.T {
		case msgData:
			select {
			case s.inbox <- Message{Job: m.Job, Body: m.Body}:
			case <-s.ctx.Done():
				return
			}
		case msgHeartbeat:
			s.jobs.beat(m.Job, s.clock.Now())
		case msgDone:
			s.jobs.done(m.Job)
		}
	}
}

// monitor reports jobs gone quiet, every HeartbeatInterval.
func (s *Session) monitor() {
	tick, stop := s.clock.Tick(HeartbeatInterval)
	defer stop()
	for {
		select {
		case <-s.done:
			return
		case <-tick:
			for _, l := range s.jobs.lost(s.clock.Now()) {
				select {
				case s.lost <- l:
				case <-s.done:
					return
				}
			}
		}
	}
}
