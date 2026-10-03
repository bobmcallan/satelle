package gatehandle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Resume-wake state (sty_eac9b28d, epic:gate-wake). A harness that caps the Stop
// continuations of a turn cannot always wake a session in-turn; its finished
// gate is then delivered by resuming the session. Three small records back that:
//
//	<gates>/.turns/<session>.stops   how many Stop emissions this turn has spent
//	<gates>/.turns/<session>.lock    the resume that is running for the session
//	<gates>/<handle>/resume-armed    a watcher owns this handle's resume
//
// None of it is a status query: nothing here answers "is it done" to a caller.

const turnsDir = ".turns"

func (s *Store) turnPath(session, suffix string) string {
	sum := sha256.Sum256([]byte(session))
	return filepath.Join(s.dir, turnsDir, hex.EncodeToString(sum[:8])+"."+suffix)
}

// StopCount is how many Stop emissions the session's current turn has spent.
func (s *Store) StopCount(session string) int {
	b, err := os.ReadFile(s.turnPath(session, "stops"))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// AddStopCount records one more Stop emission and returns the new count. The
// count is for the harness's own continuation budget, so it is bumped for every
// emission the harness counts, not only for blocks.
func (s *Store) AddStopCount(session string) int {
	n := s.StopCount(session) + 1
	if err := os.MkdirAll(filepath.Join(s.dir, turnsDir), 0o755); err != nil {
		return n
	}
	_ = writeAtomic(s.turnPath(session, "stops"), []byte(strconv.Itoa(n)))
	return n
}

// ResetStopCount starts a fresh turn's count.
func (s *Store) ResetStopCount(session string) {
	_ = os.Remove(s.turnPath(session, "stops"))
}

// HarnessSession is what a resume needs to know about the driving session,
// recorded by the hooks the harness does call (a prompt, a tool call, a Stop) so
// that a gate started later — by a tool call, after the harness stopped calling
// the Stop hook — can still be resumed into it.
type HarnessSession struct {
	Harness string `json:"harness"`
	Session string `json:"session"`
	Cwd     string `json:"cwd,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// NoteSession records the driving session for owner (the identity a handle is
// stamped with). It writes only when something changed.
func (s *Store) NoteSession(owner string, hs HarnessSession) {
	if cur, ok := s.SessionFor(owner); ok && cur == hs {
		return
	}
	b, err := json.Marshal(hs)
	if err != nil || os.MkdirAll(filepath.Join(s.dir, turnsDir), 0o755) != nil {
		return
	}
	_ = writeAtomic(s.turnPath(owner, "session"), b)
}

// SessionFor reads what NoteSession recorded for owner.
func (s *Store) SessionFor(owner string) (HarnessSession, bool) {
	var hs HarnessSession
	b, err := os.ReadFile(s.turnPath(owner, "session"))
	if err != nil || json.Unmarshal(b, &hs) != nil || hs.Session == "" {
		return HarnessSession{}, false
	}
	return hs, true
}

// OpenTurn records that the session is mid-turn as of now: a user prompt, or any
// tool call the harness announced. CloseTurn records that its Stop was allowed.
func (s *Store) OpenTurn(session string) { s.setTurn(session, "open") }

// CloseTurn records that the session's turn ended.
func (s *Store) CloseTurn(session string) { s.setTurn(session, "closed") }

func (s *Store) setTurn(session, state string) {
	if os.MkdirAll(filepath.Join(s.dir, turnsDir), 0o755) != nil {
		return
	}
	_ = writeAtomic(s.turnPath(session, "turn"), []byte(state))
}

// TurnIdle reports whether a resume may start in the session. It may when the
// turn is closed, or when nothing was recorded (no hook has spoken for it). A
// turn still open is idle only once its Stop budget is spent AND it has gone
// quiet: a harness does not call the Stop hook after the last continuation, so
// the end of that turn is not announced, and the quiet is the only sign of it.
// Until then the Stop hook is still to be consulted and delivers in-turn.
func (s *Store) TurnIdle(session string, stopCap int, quiet time.Duration) bool {
	b, err := os.ReadFile(s.turnPath(session, "turn"))
	if err != nil {
		return true
	}
	if strings.TrimSpace(string(b)) == "closed" {
		return true
	}
	st, err := os.Stat(s.turnPath(session, "turn"))
	return err == nil && s.StopCount(session) >= stopCap && time.Since(st.ModTime()) >= quiet
}

// ArmResume reports whether this caller is the one to own id's resume: exclusive
// create, so a second Stop that sees the same running gate does not start a
// second watcher. Arming also marks the run notified, so it is never dropped for
// age or pruned before its verdict is delivered.
func (s *Store) ArmResume(id string) bool {
	f, err := os.OpenFile(s.path(id, "resume-armed"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, _ = fmt.Fprintln(f, time.Now().UTC().Format(time.RFC3339))
	if f.Close() != nil {
		return false
	}
	s.MarkNotified(id, false, "")
	return true
}

// ResumeArmed reports whether a watcher already owns id's resume.
func (s *Store) ResumeArmed(id string) bool { return s.has(id, "resume-armed") }

// Disarm withdraws an arming whose watcher could not be started, so a later Stop
// can arm it again.
func (s *Store) Disarm(id string) {
	_ = os.Remove(s.path(id, "resume-armed"))
}

// Release gives a claim back, for a delivery that could not start. It is the only
// way a delivered handle becomes undelivered, and only the claimant that failed
// to deliver may use it.
func (s *Store) Release(id string) {
	_ = os.Remove(s.path(id, "delivered"))
}

// lockStale is how long a resume lock may be held before it is judged abandoned
// when its holder's liveness cannot be read: a resumed turn longer than this is
// not one to queue behind.
const lockStale = 6 * time.Hour

// LockSession takes the session's resume lock, waiting up to wait for a resume
// already running. Two gates that finish together must not resume one session
// twice at once. ok is false when the lock stayed held. A lock whose holder is
// gone is taken over.
func (s *Store) LockSession(session string, wait time.Duration) (unlock func(), ok bool) {
	if err := os.MkdirAll(filepath.Join(s.dir, turnsDir), 0o755); err != nil {
		return func() {}, false
	}
	path := s.turnPath(session, "lock")
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(path) }, true
		}
		if s.lockAbandoned(path) {
			_ = os.Remove(path)
			continue
		}
		if !time.Now().Before(deadline) {
			return func() {}, false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Store) lockAbandoned(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		if live, _ := probe(pid); live == Gone {
			return true
		}
	}
	st, err := os.Stat(path)
	return err == nil && time.Since(st.ModTime()) > lockStale
}
