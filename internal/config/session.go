package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SessionEnv is the process identity SATELLE_SESSION. Acquire stamps it on the
// lease; hooks prefer it over the harness payload so a dispatched performer
// inherits the driver's stamp rather than its own session UUID.
const SessionEnv = "SATELLE_SESSION"

// SessionFromEnv returns the trimmed SATELLE_SESSION value, or empty.
func SessionFromEnv() string {
	return strings.TrimSpace(os.Getenv(SessionEnv))
}

// PublishSession records id against this process and its parent so a later
// satelle CLI in the same harness tree (story set, etc.) can ResolveSession
// without the env being set yet. The harness session_id is the value; we do
// not invent one.
func PublishSession(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	dir := sessionPublishDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	for _, pid := range []int{os.Getpid(), os.Getppid()} {
		if pid <= 1 {
			continue
		}
		_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)), []byte(id+"\n"), 0o600)
	}
}

// ResolveSession returns the session identity for this process: SATELLE_SESSION
// if set, else a published id on this pid or an ancestor. Empty when neither
// channel has a value (ordinary unstamped use).
func ResolveSession() string {
	if id := SessionFromEnv(); id != "" {
		return id
	}
	return publishedSession()
}

// PublishSessionModel records the model a session role reported, alongside
// the executable that reported it (the cross-provider guard's evidence,
// sty_7069bced). role is "in-loop" | "orchestrator" | "creator". Unlike
// PublishSession this needs no pid-walk: the caller already knows sessionID
// (from ResolveSession/bindSessionID), so the file is keyed directly.
func PublishSessionModel(sessionID, role, model, executable string) {
	sessionID = strings.TrimSpace(sessionID)
	role = strings.TrimSpace(role)
	if sessionID == "" || role == "" {
		return
	}
	dir := sessionModelDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	body := strings.TrimSpace(model) + "\t" + strings.TrimSpace(executable) + "\n"
	_ = os.WriteFile(filepath.Join(dir, sessionModelFile(sessionID, role)), []byte(body), 0o600)
}

// ResolveSessionModel returns the model a session role previously published
// via PublishSessionModel, and the executable that reported it. Both are
// empty when nothing was published for that (sessionID, role) pair — the
// caller treats that exactly like an explicit "unknown".
func ResolveSessionModel(sessionID, role string) (model, executable string) {
	sessionID = strings.TrimSpace(sessionID)
	role = strings.TrimSpace(role)
	if sessionID == "" || role == "" {
		return "", ""
	}
	b, err := os.ReadFile(filepath.Join(sessionModelDir(), sessionModelFile(sessionID, role)))
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), "\t", 2)
	model = parts[0]
	if len(parts) > 1 {
		executable = parts[1]
	}
	return model, executable
}

// sessionModelFile is the on-disk name for one (sessionID, role) pair. role is
// a small closed set of path-safe tokens ("in-loop", "orchestrator",
// "creator"); sessionID is a harness-issued UUID-shaped id, but the dot
// separator keeps the two halves unambiguous even if that ever changes.
func sessionModelFile(sessionID, role string) string {
	return "model." + sessionID + "." + role
}

// sessionModelDir is sessionPublishDir's sibling for published session
// models — same lifecycle and permissions, kept in its own subdirectory so a
// directory listing of one is never confused with the other.
func sessionModelDir() string {
	return filepath.Join(sessionPublishDir(), "models")
}

func sessionPublishDir() string {
	if h := strings.TrimSpace(os.Getenv("SATELLE_HOME")); h != "" {
		return filepath.Join(h, "sessions")
	}
	if r := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); r != "" {
		return filepath.Join(r, "satelle-sessions")
	}
	return filepath.Join(os.TempDir(), "satelle-sessions-"+strconv.Itoa(os.Getuid()))
}

func publishedSession() string {
	dir := sessionPublishDir()
	seen := map[int]bool{}
	pid := os.Getpid()
	for i := 0; i < 16 && pid > 1 && !seen[pid]; i++ {
		seen[pid] = true
		if b, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(pid))); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return id
			}
		}
		next, ok := readParentPID(pid)
		if !ok {
			if i == 0 {
				pid = os.Getppid()
				continue
			}
			break
		}
		pid = next
	}
	return ""
}

// readParentPID reads PPid from /proc/<pid>/stat. The comm field may contain
// spaces and parentheses, so parse from the last ')' (same rule as supervisor).
func readParentPID(pid int) (int, bool) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, false
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(string(raw)[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
