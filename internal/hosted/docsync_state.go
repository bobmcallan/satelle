package hosted

// Document-sync pull cursor store (epic:scoped-sync, order:6). Persists the
// incremental changed-since cursor OUTSIDE any repo — beside the credentials
// file under $XDG_CONFIG_HOME/satelle/ — keyed by (server, project, repoRoot)
// so one login's multi-repo / multi-project pulls don't clobber each other's
// cursors. Workspace id dropped with project-addressed routes (sty_ca64d0cb).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// documentSyncStateFile is the on-disk JSON shape: document-pull cursors plus
// work-state push cursors (sty_88e83180), both keyed by (server, project, repo).
type documentSyncStateFile struct {
	Cursors   map[string]string          `json:"cursors"`
	Workstate map[string]WorkstateCursor `json:"workstate,omitempty"`
}

// WorkstateCursor tracks the high-water marks of the last successful work-state
// push for one (server, project, repoRoot) key (sty_88e83180). Zero times mean
// "never pushed" / full set next time.
type WorkstateCursor struct {
	ItemsUpdatedAt  time.Time `json:"items_updated_at"`
	LedgerCreatedAt time.Time `json:"ledger_created_at"`
}

// docSyncMu serialises read-modify-write of the state file across concurrent
// CLI invocations in the same process (tests share the XDG dir).
var docSyncMu sync.Mutex

// DocumentSyncStatePath returns the per-user document-sync cursor file:
// same directory as credentials.toml, file document-sync-state.json.
// Path overrides (tests) via DocumentSyncStatePathOverride.
func DocumentSyncStatePath() (string, error) {
	if p := strings.TrimSpace(DocumentSyncStatePathOverride); p != "" {
		return p, nil
	}
	cred, err := CredentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cred), "document-sync-state.json"), nil
}

// DocumentSyncStatePathOverride, when non-empty, replaces DocumentSyncStatePath
// (tests only). Empty in production.
var DocumentSyncStatePathOverride string

// documentCursorKey builds the map key for a (server, project, repo) triple.
// server is normalized the same way credentials are; repoRoot should be
// absolute; project is the bound hosted project id or slug.
func documentCursorKey(server, project, repoRoot string) string {
	return normalizeServerURL(server) + "|" + project + "|" + filepath.Clean(repoRoot)
}

// LoadDocumentCursor returns the last-persisted pull cursor for the key, or
// "" when none is stored (first pull / unknown key). A missing file is not an
// error — it yields the empty cursor.
func LoadDocumentCursor(server, project, repoRoot string) (string, error) {
	docSyncMu.Lock()
	defer docSyncMu.Unlock()
	state, err := loadDocumentSyncState()
	if err != nil {
		return "", err
	}
	return state.Cursors[documentCursorKey(server, project, repoRoot)], nil
}

// SaveDocumentCursor persists the pull cursor for the key. Write is atomic
// (tmp + rename) so a crash mid-write cannot corrupt the file.
func SaveDocumentCursor(server, project, repoRoot, cursor string) error {
	docSyncMu.Lock()
	defer docSyncMu.Unlock()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.Cursors == nil {
		state.Cursors = map[string]string{}
	}
	state.Cursors[documentCursorKey(server, project, repoRoot)] = cursor
	return writeDocumentSyncState(state)
}

// LoadWorkstateCursor returns the last successful work-state push cursor for
// the key, or a zero cursor when none is stored (sty_88e83180). A missing file
// is not an error.
func LoadWorkstateCursor(server, project, repoRoot string) (WorkstateCursor, error) {
	docSyncMu.Lock()
	defer docSyncMu.Unlock()
	state, err := loadDocumentSyncState()
	if err != nil {
		return WorkstateCursor{}, err
	}
	if state.Workstate == nil {
		return WorkstateCursor{}, nil
	}
	return state.Workstate[documentCursorKey(server, project, repoRoot)], nil
}

// SaveWorkstateCursor persists the work-state push cursor for the key. Atomic
// tmp+rename; advances only on a confirmed successful push (sty_88e83180 AC3).
func SaveWorkstateCursor(server, project, repoRoot string, c WorkstateCursor) error {
	docSyncMu.Lock()
	defer docSyncMu.Unlock()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.Workstate == nil {
		state.Workstate = map[string]WorkstateCursor{}
	}
	state.Workstate[documentCursorKey(server, project, repoRoot)] = c
	return writeDocumentSyncState(state)
}

// loadDocumentSyncState reads the state file. A missing file yields an empty map.
func loadDocumentSyncState() (documentSyncStateFile, error) {
	path, err := DocumentSyncStatePath()
	if err != nil {
		return documentSyncStateFile{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return documentSyncStateFile{Cursors: map[string]string{}}, nil
		}
		return documentSyncStateFile{}, fmt.Errorf("hosted: read document sync state: %w", err)
	}
	var state documentSyncStateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return documentSyncStateFile{}, fmt.Errorf("hosted: decode document sync state: %w", err)
	}
	if state.Cursors == nil {
		state.Cursors = map[string]string{}
	}
	return state, nil
}

// writeDocumentSyncState writes the state file atomically under its directory.
func writeDocumentSyncState(state documentSyncStateFile) error {
	path, err := DocumentSyncStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("hosted: create document sync state dir: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("hosted: encode document sync state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("hosted: write document sync state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosted: rename document sync state: %w", err)
	}
	return nil
}
