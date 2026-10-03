package gatehandle

import (
	"os"
	"path/filepath"
	"strings"
)

// Cross-repo handles (sty_8f10499d, epic:gate-wake). A session's hooks read the
// store of the repo they fire in, but a gate it starts can act on another repo
// and so be written into that repo's store, where the session's hooks never look.
// The serving repo therefore keeps a pointer to each such handle:
//
//	<gates>/.xrepo/<session>/<handle>   the runtime dir of the store that holds it
//
// A pointer is a route to a handle, never a second copy of it: the handle, its
// claim and its delivery stay in the store that holds it, so the one exclusive
// Claim there still makes a verdict reach the session once. Nothing here answers
// "is it done" to a caller.

const forwardDir = ".xrepo"

// Forward is one cross-repo handle a session's serving store points at.
type Forward struct {
	ID         string
	RuntimeDir string
}

func (s *Store) forwardPath(session, id string) string {
	return filepath.Join(s.dir, forwardDir, sessionKey(session), id)
}

// Forward records that session's handle id lives in the store at runtimeDir.
func (s *Store) Forward(session, id, runtimeDir string) error {
	if err := os.MkdirAll(filepath.Dir(s.forwardPath(session, id)), 0o755); err != nil {
		return err
	}
	return writeAtomic(s.forwardPath(session, id), []byte(runtimeDir))
}

// Forwards lists the pointers recorded for session.
func (s *Store) Forwards(session string) []Forward {
	entries, err := os.ReadDir(filepath.Join(s.dir, forwardDir, sessionKey(session)))
	if err != nil {
		return nil
	}
	var out []Forward
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), Prefix) {
			continue
		}
		b, err := os.ReadFile(s.forwardPath(session, e.Name()))
		if dir := strings.TrimSpace(string(b)); err == nil && dir != "" {
			out = append(out, Forward{ID: e.Name(), RuntimeDir: dir})
		}
	}
	return out
}

// Unforward drops a pointer whose handle is delivered or gone.
func (s *Store) Unforward(session, id string) {
	_ = os.Remove(s.forwardPath(session, id))
}
