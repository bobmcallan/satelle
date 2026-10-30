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
	// Snapshots records, per (server, project, repo, area), the hosted snapshot
	// this checkout last synced from (sty_fe5a8ed4). It lives here, outside any
	// repo tree, so knowing what was last synced never becomes a file that is
	// itself synced.
	Snapshots map[string]AreaBase `json:"snapshots,omitempty"`
	// SnapshotTokens holds, per (server, project, repo), where this checkout's
	// work-state Snapshot pass stands (sty_2cb4e1f6).
	SnapshotTokens map[string]SnapshotToken `json:"snapshot_tokens,omitempty"`
	// AttachmentsUploaded records, per (server, project, repo), the content hash of
	// each story attachment this machine has confirmed in the hosted documents
	// store ("<id>/<file>" → sha256 hex). It is the selector of which attachments
	// still need uploading, so it is independent of the work-state cursors: an
	// attachment behind them, or one whose ledger row never travelled, is found by
	// its hash alone (sty_4d42e13e).
	AttachmentsUploaded map[string]map[string]string `json:"attachments_uploaded,omitempty"`
	// AttachmentCursors is the documents changed-since cursor of the last
	// attachment pull, per (server, project, repo), kept apart from the document
	// pull's own cursor.
	AttachmentCursors map[string]string `json:"attachment_cursors,omitempty"`
}

// SnapshotToken is a checkout's work-state Snapshot position for one (server,
// project, repo). Token is the sync_token of the last completed pass, sent as
// since_token on the next; it changes only when a pass finishes. Areas is the
// set of opted-in work-state areas that pass covered — a token saved for other
// areas would skip rows the new set needs, so a mismatch discards it. StoreID is
// the local database the pass landed in: a replaced database holds none of those
// rows, so a token from another one is discarded too. The
// Resume* fields checkpoint a pass cut short (timeout, error): the next pass
// continues from ResumePageToken, sending ResumeSince as it began. Pending
// maps a story id to the hosted status not yet forwarded to the local row,
// carried so the end of a resumed pass can retry it once the pass's ledger
// rows have landed.
type SnapshotToken struct {
	Token           string            `json:"token,omitempty"`
	Areas           string            `json:"areas,omitempty"`
	StoreID         string            `json:"store_id,omitempty"`
	ResumePageToken string            `json:"resume_page_token,omitempty"`
	ResumeSince     string            `json:"resume_since,omitempty"`
	Pending         map[string]string `json:"pending,omitempty"`
	SavedAt         time.Time         `json:"saved_at"`
}

// LoadSnapshotToken returns the stored Snapshot position for the key, or the
// zero value when none is stored. A missing file is not an error.
func LoadSnapshotToken(server, project, repoRoot string) (SnapshotToken, error) {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return SnapshotToken{}, err
	}
	return state.SnapshotTokens[documentCursorKey(server, project, repoRoot)], nil
}

// SaveSnapshotToken persists the Snapshot position for the key (atomic).
func SaveSnapshotToken(server, project, repoRoot string, t SnapshotToken) error {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.SnapshotTokens == nil {
		state.SnapshotTokens = map[string]SnapshotToken{}
	}
	state.SnapshotTokens[documentCursorKey(server, project, repoRoot)] = t
	return writeDocumentSyncState(state)
}

// ClearSnapshotToken drops the Snapshot position for the key, so the next pass
// is a full one.
func ClearSnapshotToken(server, project, repoRoot string) error {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	key := documentCursorKey(server, project, repoRoot)
	if _, ok := state.SnapshotTokens[key]; !ok {
		return nil
	}
	delete(state.SnapshotTokens, key)
	return writeDocumentSyncState(state)
}

// AreaBase is what one checkout last synced for one sync area: the effective
// snapshot version, the sha of every file as of that snapshot, and the paths a
// pull left unmerged because they changed on both sides. It is the "base" of the
// three-way comparison a pull makes, and the version a push must still match.
type AreaBase struct {
	Version  int               `json:"version"`
	Files    map[string]string `json:"files,omitempty"`
	Unmerged map[string]string `json:"unmerged,omitempty"`
	// Defaults are hosted files (path -> sha) a pull proved to be unedited
	// embedded defaults the installed binary supplies, in a form the binary cannot
	// predict from its own embedded bodies (an older stamp). Such a path is never
	// written to the tree, so its sha is kept here to be recognised without a fetch.
	Defaults map[string]string `json:"defaults,omitempty"`
	// Seal is the sync driver's proof that it wrote this base (snapsync): a base
	// set or moved by anything else carries none, or one that no longer matches.
	// SaveAreaBase stores it as given.
	Seal string `json:"seal,omitempty"`
}

func areaBaseKey(server, project, repoRoot, area string) string {
	return documentCursorKey(server, project, repoRoot) + "|" + area
}

// LoadAreaBase returns the recorded base for an area. ok=false means this
// checkout has never synced the area against this project.
func LoadAreaBase(server, project, repoRoot, area string) (AreaBase, bool, error) {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return AreaBase{}, false, err
	}
	b, ok := state.Snapshots[areaBaseKey(server, project, repoRoot, area)]
	return b, ok, nil
}

// SaveAreaBase persists the base for an area (atomic, like every state write).
func SaveAreaBase(server, project, repoRoot, area string, b AreaBase) error {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.Snapshots == nil {
		state.Snapshots = map[string]AreaBase{}
	}
	state.Snapshots[areaBaseKey(server, project, repoRoot, area)] = b
	return writeDocumentSyncState(state)
}

// WorkstateCursor tracks the high-water marks of the last successful work-state
// push for one (server, project, repoRoot) key (sty_88e83180). Zero times mean
// "never pushed" / full set next time.
type WorkstateCursor struct {
	ItemsUpdatedAt time.Time `json:"items_updated_at"`
	// LedgerCreatedAt is the newest created_at pushed. Informational since
	// sty_4a31e1ed: the ledger is paged by insertion order below, because a row
	// can commit with a created_at behind one already pushed. A cursor with only
	// this set (written by an older binary) is a fresh start for the ledger.
	LedgerCreatedAt time.Time `json:"ledger_created_at"`
	// LedgerSeq is the insertion position (rowid) of the last ledger row pushed;
	// LedgerAnchorID is that row's id and LedgerStoreID the database it was read
	// from. All three must still match the local store, or the position belongs
	// to a replaced or renumbered database and the ledger starts over.
	LedgerSeq      int64  `json:"ledger_seq,omitempty"`
	LedgerAnchorID string `json:"ledger_anchor_id,omitempty"`
	LedgerStoreID  string `json:"ledger_store_id,omitempty"`
	// StoryHeads maps a story id to the last of its events this server accepted from
	// this checkout (sty_9bdf08c4): the base the next Chain/Append names. It lives
	// here so a rebind or another server starts a story from an empty base rather
	// than a head the new project never saw. SaveStoryHead writes it per story;
	// SaveWorkstateCursor leaves it alone unless the cursor it is given carries one.
	StoryHeads map[string]string `json:"story_heads,omitempty"`
}

// docSyncMu serialises read-modify-write of the state file across concurrent
// CLI invocations in the same process (tests share the XDG dir).
var docSyncMu sync.Mutex

// lockDocSyncState serialises a read-modify-write of the state file against every
// other writer: goroutines of this process through docSyncMu, and other satelle
// processes (satelled, another repo's sync) through an advisory lock on a sibling
// file. The file is shared by every checkout of the user, so a lone mutex lets two
// processes overwrite each other's change. A lock file that cannot be opened
// leaves the in-process guard only; the write itself stays atomic either way.
// The returned func releases both.
func lockDocSyncState() func() {
	docSyncMu.Lock()
	path, err := DocumentSyncStatePath()
	if err != nil {
		return docSyncMu.Unlock
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return docSyncMu.Unlock
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return docSyncMu.Unlock
	}
	if err := lockStateFile(f); err != nil {
		_ = f.Close()
		return docSyncMu.Unlock
	}
	return func() {
		unlockStateFile(f)
		_ = f.Close()
		docSyncMu.Unlock()
	}
}

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
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return "", err
	}
	return state.Cursors[documentCursorKey(server, project, repoRoot)], nil
}

// SaveDocumentCursor persists the pull cursor for the key. Write is atomic
// (tmp + rename) so a crash mid-write cannot corrupt the file.
func SaveDocumentCursor(server, project, repoRoot, cursor string) error {
	defer lockDocSyncState()()
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
	defer lockDocSyncState()()
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
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.Workstate == nil {
		state.Workstate = map[string]WorkstateCursor{}
	}
	key := documentCursorKey(server, project, repoRoot)
	if c.StoryHeads == nil {
		c.StoryHeads = state.Workstate[key].StoryHeads
	}
	state.Workstate[key] = c
	return writeDocumentSyncState(state)
}

// SaveStoryHead records eventID as the last event of storyID the server accepted
// for the key, leaving the rest of the cursor as it was.
func SaveStoryHead(server, project, repoRoot, storyID, eventID string) error {
	return SaveStoryHeads(server, project, repoRoot, map[string]string{storyID: eventID})
}

// SaveStoryHeads records several stories' heads in one write of the state file,
// which is rewritten whole and so is not worth rewriting once per story.
func SaveStoryHeads(server, project, repoRoot string, set map[string]string) error {
	if len(set) == 0 {
		return nil
	}
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.Workstate == nil {
		state.Workstate = map[string]WorkstateCursor{}
	}
	key := documentCursorKey(server, project, repoRoot)
	c := state.Workstate[key]
	heads := make(map[string]string, len(c.StoryHeads)+len(set))
	for id, head := range c.StoryHeads {
		heads[id] = head
	}
	for id, head := range set {
		heads[id] = head
	}
	c.StoryHeads = heads
	state.Workstate[key] = c
	return writeDocumentSyncState(state)
}

// LoadUploadedAttachments returns the attachment hashes this machine has confirmed
// hosted for the key ("<id>/<file>" → sha256 hex). A missing file or key is empty.
func LoadUploadedAttachments(server, project, repoRoot string) (map[string]string, error) {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range state.AttachmentsUploaded[documentCursorKey(server, project, repoRoot)] {
		out[k] = v
	}
	return out, nil
}

// RecordUploadedAttachments merges confirmed attachment hashes into the key's
// record in one atomic write. Call it only for files the server confirmed (or
// whose hosted copy is known identical), so a failed upload stays unrecorded and
// is retried by the next push.
func RecordUploadedAttachments(server, project, repoRoot string, confirmed map[string]string) error {
	if len(confirmed) == 0 {
		return nil
	}
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.AttachmentsUploaded == nil {
		state.AttachmentsUploaded = map[string]map[string]string{}
	}
	key := documentCursorKey(server, project, repoRoot)
	m := state.AttachmentsUploaded[key]
	if m == nil {
		m = map[string]string{}
	}
	for k, v := range confirmed {
		m[k] = v
	}
	state.AttachmentsUploaded[key] = m
	return writeDocumentSyncState(state)
}

// LoadAttachmentCursor returns the stored attachment pull cursor, or "" when none.
func LoadAttachmentCursor(server, project, repoRoot string) (string, error) {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return "", err
	}
	return state.AttachmentCursors[documentCursorKey(server, project, repoRoot)], nil
}

// SaveAttachmentCursor persists the attachment pull cursor for the key.
func SaveAttachmentCursor(server, project, repoRoot, cursor string) error {
	defer lockDocSyncState()()
	state, err := loadDocumentSyncState()
	if err != nil {
		return err
	}
	if state.AttachmentCursors == nil {
		state.AttachmentCursors = map[string]string{}
	}
	state.AttachmentCursors[documentCursorKey(server, project, repoRoot)] = cursor
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
	// A temp file of its own, never a fixed name: two writers sharing one name
	// truncate each other's bytes and one rename finds the file already moved.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("hosted: write document sync state: %w", err)
	}
	tmp := f.Name()
	_, err = f.Write(append(raw, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosted: write document sync state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosted: rename document sync state: %w", err)
	}
	return nil
}
