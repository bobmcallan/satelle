// Package gatehandle is the runtime record of a gate run that was handed off to
// a detached process (sty_c4b92c9e, epic:token-accountability).
//
// A gate-running verb called by an agent must return before the driving
// harness's background cutoff — a backgrounded command makes the driver
// re-enter the model to learn how it ended, and each re-entry is a full model
// call. So the verb starts the real run detached, waits a bounded time for it,
// and either returns the finished verdict block or a handle. This package is
// where that run leaves its outcome.
//
// The shape is deliberately write-only from the caller's side. Nothing here is
// a status query: the one reader of a finished, undelivered handle is the
// harness-hook delivery (Claim), which hands each outcome to the session
// exactly once. A verb that answered "pending" or "done" for a handle would
// give the driver something to poll, and a poll is the cost this exists to
// remove.
//
// Layout, under <runtime dir>/gates/<handle>/:
//
//	meta.json     what was started (Meta)
//	out, err      the run's stdout and stderr — diagnostics, never delivered
//	verdict.log   the reviewers' verdict lines, one per gate — what is delivered
//	progress.log  the reviewer's progress lines, kept off the agent's stream
//	result.json   written once, atomically, when the run ends (Result)
//	delivered     created exclusively by Claim; its existence is "already told"
//	pending-notified     the Stop hook told the session the run was still going
//	unverified-notified  the same, when liveness could not be verified (holds why)
package gatehandle

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// EnvHandle marks a process as the detached run of a handle: it carries the
// handle id. Its presence is what tells the CLI not to hand off again.
const EnvHandle = "SATELLE_GATE_HANDLE"

// EnvRuntime carries the runtime dir the handle lives under, so the detached
// run can record its result without reopening the store.
const EnvRuntime = "SATELLE_GATE_RUNTIME"

// Prefix leads every handle id.
const Prefix = "gw_"

// Meta is what was started.
type Meta struct {
	ID    string   `json:"id"`
	Verb  string   `json:"verb"`
	Story string   `json:"story,omitempty"`
	Argv  []string `json:"argv"`
	PID   int      `json:"pid"`
	// PIDStart is the process's creation identity, recorded with the pid: a pid
	// alone can be reused by another process once the run is gone, and a session
	// must not wait on a stranger. Empty when the platform cannot read one, which
	// IdentityUnavailable records.
	PIDStart            string    `json:"pid_start,omitempty"`
	IdentityUnavailable bool      `json:"identity_unavailable,omitempty"`
	Started             time.Time `json:"started"`
	// Harness is the driving harness the run was started for ("" when none was
	// detected) — what the pending line quoted a limitation for.
	Harness string `json:"harness,omitempty"`
	// Session is the driving session's identity when one is resolvable: a
	// delivery hook tells a session about its own runs, never a sibling's.
	Session string `json:"session,omitempty"`
}

// Result is how the run ended.
type Result struct {
	ExitCode int       `json:"exit_code"`
	Error    string    `json:"error,omitempty"`
	Finished time.Time `json:"finished"`
}

// State of a handle, derived from what is on disk and whether the run's
// process is still alive.
type State string

const (
	// Running: no result yet and the process is alive.
	Running State = "running"
	// Finished: the run recorded a Result.
	Finished State = "finished"
	// Died: no result and the run can no longer record one — its process is gone
	// (or a stranger holds its pid), it was never started, or its record is
	// unreadable. Delivered like a failure, never left dangling.
	Died State = "died"
	// RunningUnverified: no result, and this platform could not say whether the
	// process is the run's own (liveness or identity unavailable). Its verdict is
	// delivered when it finishes, but it never holds a session waiting: see
	// Store.MarkNotified.
	RunningUnverified State = "running-unverified"
)

// Store is the handles directory of one repo's runtime plane.
type Store struct {
	dir string
}

// New returns the store rooted at <runtimeDir>/gates. Nothing is created until
// the first Create.
func New(runtimeDir string) *Store {
	return &Store{dir: filepath.Join(runtimeDir, "gates")}
}

// NewID mints a handle id.
func NewID() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return Prefix + hex.EncodeToString(b[:])
}

// Dir is the directory the handles live under.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(id string, parts ...string) string {
	return filepath.Join(append([]string{s.dir, id}, parts...)...)
}

// Create records a new handle and returns it. Meta.PID is filled by SetPID once
// the process has started.
func (s *Store) Create(m Meta) (Meta, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.Started.IsZero() {
		m.Started = time.Now().UTC()
	}
	if err := os.MkdirAll(s.path(m.ID), 0o755); err != nil {
		return Meta{}, err
	}
	s.prune(time.Now())
	return m, s.writeMeta(m)
}

// retention is how long a delivered handle's files are kept — enough to look at
// a verdict after the fact, short enough that the directory does not grow with
// every gate the repo ever ran. A handle never delivered is kept twice as long.
const retention = 7 * 24 * time.Hour

// prune removes handles past retention. Best-effort: a handle that cannot be
// removed stays for the next Create.
func (s *Store) prune(now time.Time) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), Prefix) {
			continue
		}
		age := 2 * retention
		limit := 2 * retention
		if st, err := os.Stat(s.path(e.Name(), "delivered")); err == nil {
			age, limit = now.Sub(st.ModTime()), retention
		} else if m, err := s.Meta(e.Name()); err == nil {
			// A session was told this run is still going, or it is running now:
			// its verdict is still owed, whatever its age.
			if s.notified(e.Name()) || s.State(e.Name()) == Running {
				continue
			}
			age = now.Sub(m.Started)
		} else {
			continue
		}
		if age > limit {
			_ = os.RemoveAll(s.path(e.Name()))
		}
	}
}

// MaxDeliveryAge bounds how old a run may be and still be handed to a session:
// a verdict from yesterday's dead session is noise, not a notification. A run a
// Stop hook already told its session about is exempt (MarkNotified).
const MaxDeliveryAge = 24 * time.Hour

// SetPID records the process running the handle and its creation identity. The
// caller runs it right after starting the process and before waiting on it, so
// the pid cannot have been reused yet and the identity read is the run's own.
func (s *Store) SetPID(id string, pid int) error {
	m, err := s.Meta(id)
	if err != nil {
		return err
	}
	m.PID = pid
	_, m.PIDStart = probe(pid)
	m.IdentityUnavailable = m.PIDStart == ""
	return s.writeMeta(m)
}

func (s *Store) writeMeta(m Meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeAtomic(s.path(m.ID, "meta.json"), b)
}

// Meta reads what was started.
func (s *Store) Meta(id string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(s.path(id, "meta.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// OutPath, ErrPath and ProgressPath are where the run's streams go; the parent
// opens them for the child before it starts.
func (s *Store) OutPath(id string) string      { return s.path(id, "out") }
func (s *Store) ErrPath(id string) string      { return s.path(id, "err") }
func (s *Store) ProgressPath(id string) string { return s.path(id, "progress.log") }

// VerdictPath is where the detached run leaves its verdict lines. A run's stdout
// is the record the command printed (a whole story, for a status change) and its
// stderr is advisory notes; neither is the verdict, and handing them to a session
// costs it tokens it cannot act on.
func (s *Store) VerdictPath(id string) string { return s.path(id, "verdict.log") }

// Finish records how the run ended. It is written once: a second call is a
// no-op, so a run that records its own result is never overwritten by a
// delivery that noticed its process gone.
func (s *Store) Finish(id string, r Result) error {
	if r.Finished.IsZero() {
		r.Finished = time.Now().UTC()
	}
	if _, err := os.Stat(s.path(id, "result.json")); err == nil {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeAtomic(s.path(id, "result.json"), b)
}

// Result reads how the run ended; ok is false while it has not.
func (s *Store) Result(id string) (Result, bool) {
	var r Result
	b, err := os.ReadFile(s.path(id, "result.json"))
	if err != nil || json.Unmarshal(b, &r) != nil {
		return Result{}, false
	}
	return r, true
}

// startGrace is how long a handle may sit with no process recorded before it is
// judged dead: the parent records the pid right after starting the run, so a
// handle still without one after this long lost its parent in between.
const startGrace = 30 * time.Second

// Liveness is what a platform could say about a process.
type Liveness int

const (
	// Gone: no such process (a zombie counts).
	Gone Liveness = iota
	// Alive: the process exists.
	Alive
	// Unknown: the platform could not tell — access denied, or the test knob.
	Unknown
)

// EnvLiveness, set to "unavailable", makes every probe answer Unknown with no
// identity. It exists to exercise the platforms-cannot-tell path on a host that
// can; it is not an operator feature.
const EnvLiveness = "SATELLE_GATE_LIVENESS"

// probe reports whether pid is alive and, when the platform can read one, the
// process's creation identity ("" when it cannot). A var so tests can stand in
// for a process that has gone, or been replaced.
var probe = probeProcess

func probeProcess(pid int) (Liveness, string) {
	if strings.TrimSpace(os.Getenv(EnvLiveness)) == "unavailable" {
		return Unknown, ""
	}
	if pid <= 0 {
		return Gone, ""
	}
	return osProbe(pid)
}

// Reasons a handle Died, and why liveness is unverified. They are delivered, so
// each says what happened.
const (
	reasonExited   = "the gate run's process exited without recording a result"
	reasonReused   = "the gate run's process id was reused by another process, so it exited without recording a result"
	reasonNoStart  = "the gate run was never started"
	reasonNoMeta   = "the gate run's record is unreadable"
	unverifiedNoID = "the run's process identity cannot be read"
	unverifiedNoLV = "the run's process cannot be inspected"
)

// Observation is one look at a handle. Taking one look and deciding from it —
// rather than asking State and then Load — is what keeps a run that finishes
// between two questions from being answered "running" to one and "done" to the
// other.
type Observation struct {
	State State
	// Reason is why the run Died, or why its liveness is unverified.
	Reason string
	// Verdict is the outcome, set when State is Finished or Died.
	Verdict Verdict
}

// Terminal reports whether the run is over and Verdict is its outcome.
func (o Observation) Terminal() bool { return o.State == Finished || o.State == Died }

// State derives the handle's state.
func (s *Store) State(id string) State { return s.Observe(id).State }

// Observe looks at the handle once.
func (s *Store) Observe(id string) Observation {
	if _, ok := s.Result(id); ok {
		return s.finished(id)
	}
	m, err := s.Meta(id)
	if err != nil {
		return s.died(id, Meta{ID: id}, reasonNoMeta)
	}
	if m.PID <= 0 {
		// Created but the process has not been recorded yet: the parent is still
		// starting it — unless it never got to.
		if time.Since(m.Started) > startGrace {
			return s.died(id, m, reasonNoStart)
		}
		return Observation{State: Running}
	}
	live, start := probe(m.PID)
	switch {
	case live == Unknown:
		return Observation{State: RunningUnverified, Reason: unverifiedReason(unverifiedNoLV)}
	case live == Alive && m.PIDStart == "":
		return Observation{State: RunningUnverified, Reason: unverifiedReason(unverifiedNoID)}
	case live == Alive && start == m.PIDStart:
		return Observation{State: Running}
	}
	// The process is gone, or a stranger holds its pid. The run records its result
	// before it exits, so look once more: a run that finished between the checks
	// above is finished, not dead.
	if _, ok := s.Result(id); ok {
		return s.finished(id)
	}
	if live == Alive {
		return s.died(id, m, reasonReused)
	}
	return s.died(id, m, reasonExited)
}

func unverifiedReason(why string) string {
	return fmt.Sprintf("liveness unavailable on %s/%s: %s", runtime.GOOS, runtime.GOARCH, why)
}

func (s *Store) finished(id string) Observation {
	m, err := s.Meta(id)
	if err != nil {
		m = Meta{ID: id}
	}
	r, _ := s.Result(id)
	return Observation{State: Finished, Verdict: s.verdict(id, m, r, false)}
}

func (s *Store) died(id string, m Meta, reason string) Observation {
	r := Result{ExitCode: 1, Error: reason, Finished: time.Now().UTC()}
	return Observation{State: Died, Reason: reason, Verdict: s.verdict(id, m, r, true)}
}

func (s *Store) verdict(id string, m Meta, r Result, died bool) Verdict {
	return Verdict{Meta: m, Result: r, Died: died, Lines: readFile(s.VerdictPath(id)), Stdout: readFile(s.OutPath(id)), Stderr: readFile(s.ErrPath(id))}
}

// MarkNotified records that a Stop hook told the session this run is still going:
// the run is then never dropped for age or pruned before its verdict is
// delivered. unverified, with the reason liveness could not be checked, also
// records that the session was told it cannot rely on a wait for this run, so no
// later Stop holds it — the verdict still arrives with the next prompt or Stop
// once the run has finished.
func (s *Store) MarkNotified(id string, unverified bool, reason string) {
	s.mark(id, "pending-notified", time.Now().UTC().Format(time.RFC3339))
	if unverified {
		s.mark(id, "unverified-notified", reason)
	}
}

func (s *Store) mark(id, name, body string) {
	f, err := os.OpenFile(s.path(id, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, body)
	_ = f.Close()
}

func (s *Store) notified(id string) bool { return s.has(id, "pending-notified") }

// UnverifiedNotified reports whether a Stop hook already told the session that
// liveness cannot be verified for id.
func (s *Store) UnverifiedNotified(id string) bool { return s.has(id, "unverified-notified") }

func (s *Store) has(id, name string) bool {
	_, err := os.Stat(s.path(id, name))
	return err == nil
}

// Verdict is the outcome of a finished handle. Lines is the verdict — what each
// gate decided — and is what a session is handed. Stdout and Stderr are the
// run's own streams, kept for diagnosis and for reading a value the command
// produced (the id a create printed); they are not delivered whole.
type Verdict struct {
	Meta   Meta
	Result Result
	Died   bool
	Lines  string
	Stdout string
	Stderr string
}

// Load reads a finished (or died) handle's outcome. ok is false while it is
// still running.
func (s *Store) Load(id string) (Verdict, bool) {
	o := s.Observe(id)
	return o.Verdict, o.Terminal()
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// Claim marks id delivered and reports whether this caller was the one to do it.
// It is the exactly-once seam: exclusive create, so two hooks racing on one
// finished handle cannot both tell the session.
func (s *Store) Claim(id string) bool {
	f, err := os.OpenFile(s.path(id, "delivered"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, _ = fmt.Fprintln(f, time.Now().UTC().Format(time.RFC3339))
	return f.Close() == nil
}

// Delivered reports whether id has been handed to a session.
func (s *Store) Delivered(id string) bool {
	_, err := os.Stat(s.path(id, "delivered"))
	return err == nil
}

// Undelivered lists the handles not yet handed to a session, oldest first.
// Whether each is still running is State's answer.
func (s *Store) Undelivered() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	type item struct {
		id      string
		started time.Time
	}
	var items []item
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), Prefix) || s.Delivered(e.Name()) {
			continue
		}
		m, err := s.Meta(e.Name())
		if err != nil || (!s.notified(e.Name()) && time.Since(s.endOf(e.Name(), m)) > MaxDeliveryAge) {
			continue
		}
		items = append(items, item{e.Name(), m.Started})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].started.Before(items[j].started) })
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return ids
}

// endOf is when the run's age is counted from: the moment it finished, so a long
// gate that ends just now is news, however long ago it began.
func (s *Store) endOf(id string, m Meta) time.Time {
	if r, ok := s.Result(id); ok && !r.Finished.IsZero() {
		return r.Finished
	}
	return m.Started
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
