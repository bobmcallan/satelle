package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ChangeEvent is the body POSTed by the CLI push publisher (sty_126228b2).
type ChangeEvent struct {
	RepoKey string `json:"repo_key"`
	Topic   string `json:"topic"`
	Entity  string `json:"entity"`
	At      string `json:"at"`
}

// Snapshot is a full-state push for one partition (order:4 reconcile).
// Every kind the UI reads must arrive here so serve never opens a repo DB.
type Snapshot struct {
	RepoKey      string            `json:"repo_key"`
	Slug         string            `json:"slug,omitempty"`
	Stories      []json.RawMessage `json:"stories,omitempty"`
	Tasks        []json.RawMessage `json:"tasks,omitempty"`
	Executions   []json.RawMessage `json:"executions,omitempty"`
	Docs         []json.RawMessage `json:"docs,omitempty"`
	LedgerEvents []json.RawMessage `json:"ledger_events,omitempty"`
	StoryDocs    []json.RawMessage `json:"story_docs,omitempty"` // id = story_id/name
	Seats        []json.RawMessage `json:"seats,omitempty"`
	Settings     json.RawMessage   `json:"settings,omitempty"` // single settings blob
	// Identity is the per-partition meta blob (project name, repo path, footer email).
	// Ingested as kind "identity" id "meta" (sty_400c022b / epic:mirror-ui-parity).
	Identity json.RawMessage `json:"identity,omitempty"`
}

// IdentityMeta is the JSON shape of Snapshot.Identity — what the mirror footer
// and account strip render without opening a repo DB or calling git.
type IdentityMeta struct {
	ProjectName string `json:"project_name"`
	RepoRoot    string `json:"repo_root"`
	FooterEmail string `json:"footer_email"`
}

// IngestHandler serves POST /ingest/change and POST /ingest/snapshot.
// onChange is optional (SSE doorbell).
type IngestHandler struct {
	Store    *Store
	OnChange func(topic string)
}

// Mount registers ingest routes on mux.
func (h *IngestHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/ingest/change", h.handleChange)
	mux.HandleFunc("/ingest/snapshot", h.handleSnapshot)
}

func (h *IngestHandler) handleChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var ev ChangeEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(ev.RepoKey) == "" {
		http.Error(w, "repo_key required", http.StatusBadRequest)
		return
	}
	seq, err := h.Store.ApplyChange(r.Context(), ev.RepoKey, ev.Topic, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.OnChange != nil {
		h.OnChange(ev.Topic)
		h.OnChange("projects")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "seq": seq})
}

func (h *IngestHandler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var snap Snapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(snap.RepoKey) == "" {
		http.Error(w, "repo_key required", http.StatusBadRequest)
		return
	}
	now := time.Now()
	ctx := r.Context()
	// Fail-closed: landing /r/<slug>/ must map to one partition (sty_57d5ce25).
	// Empty slug skips — the UI falls back to unique repo_key.
	if slug := strings.TrimSpace(snap.Slug); slug != "" {
		if existing, ok, err := h.Store.FindBySlug(ctx, slug); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if ok && existing.RepoKey != snap.RepoKey {
			msg := slugConflictMessage(h.Store, ctx, slug, existing.RepoKey, snap.RepoKey)
			http.Error(w, msg, http.StatusConflict)
			return
		}
	}
	if _, err := h.Store.TouchPartition(ctx, snap.RepoKey, snap.Slug, now); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	replacements := []struct {
		kind string
		rows []ItemRow
	}{
		{"story", rawToRows(snap.Stories, "id")},
		{"task", rawToRows(snap.Tasks, "id")},
		{"execution", rawToRows(snap.Executions, "id")},
		{"doc", rawToRows(snap.Docs, "name")},
		{"ledger_event", rawToRows(snap.LedgerEvents, "id")},
		{"story_doc", rawToRows(snap.StoryDocs, "id")},
		{"seat", rawToRows(snap.Seats, "id")},
	}
	for _, r := range replacements {
		if err := h.Store.ReplaceKind(ctx, snap.RepoKey, r.kind, r.rows, now); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if len(snap.Settings) > 0 {
		if err := h.Store.ReplaceKind(ctx, snap.RepoKey, "settings", []ItemRow{{ID: "config", Payload: string(snap.Settings)}}, now); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if len(snap.Identity) > 0 {
		if err := h.Store.ReplaceKind(ctx, snap.RepoKey, "identity", []ItemRow{{ID: "meta", Payload: string(snap.Identity)}}, now); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if h.OnChange != nil {
		h.OnChange("stories")
		h.OnChange("tasks")
		h.OnChange("docs")
		h.OnChange("projects")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// slugConflictMessage builds the 409 body for a landing-slug collision: names
// the slug, existing and incoming repo_keys, best-effort path of the existing
// partition, and the rename / re-seed remedy (sty_57d5ce25).
func slugConflictMessage(store *Store, ctx context.Context, slug, existingKey, incomingKey string) string {
	pathNote := ""
	if payload, err := store.GetItem(ctx, existingKey, "identity", "meta"); err == nil {
		var meta IdentityMeta
		if json.Unmarshal([]byte(payload), &meta) == nil && strings.TrimSpace(meta.RepoRoot) != "" {
			pathNote = fmt.Sprintf(" (path %s)", meta.RepoRoot)
		}
	}
	return fmt.Sprintf(
		"slug %q already used by partition %s%s; incoming repo_key %s collides. "+
			"Rename this repo's directory so its basename is unique, then re-run `satelle workspace add`. "+
			"To drop the existing landing card, remove that partition from the serve mirror.",
		slug, existingKey, pathNote, incomingKey,
	)
}

func rawToRows(raw []json.RawMessage, idKey string) []ItemRow {
	var out []ItemRow
	for _, r := range raw {
		var m map[string]any
		id := ""
		if json.Unmarshal(r, &m) == nil {
			if v, ok := m[idKey].(string); ok {
				id = v
			}
		}
		if id == "" {
			continue
		}
		out = append(out, ItemRow{ID: id, Payload: string(r)})
	}
	return out
}
