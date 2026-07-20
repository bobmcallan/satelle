// Snapshot build + POST helpers for the push-fed local UI mirror
// (sty_1dde0d47 / sty_805bee9c). The operator verb is `satelle workspace add`
// (register + seed); mutating verbs drain via uiDrain. The retired `ui push`
// spelling points at workspace add (retiredNames).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/substrate"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// postUISnapshot POSTs snap to endpoint/ingest/snapshot with a 30s budget.
// Used by workspace add (fail-visible; large snapshots OK).
func postUISnapshot(endpoint string, snap *mirror.Snapshot) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return postUISnapshotContext(ctx, endpoint, snap)
}

// postUISnapshotContext POSTs snap under the caller's deadline. The end-of-
// invocation drain (sty_9ba3d709) passes a short shared budget so a black-holed
// endpoint cannot hang the verb.
func postUISnapshotContext(ctx context.Context, endpoint string, snap *mirror.Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/") + "/ingest/snapshot"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ui push: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Surface a bounded body so slug-conflict 409 (sty_57d5ce25) and other
		// ingest rejections print a clear remedy through workspace add.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return fmt.Errorf("ui push: server returned %s: %s", resp.Status, msg)
		}
		return fmt.Errorf("ui push: server returned %s", resp.Status)
	}
	return nil
}

func buildUISnapshot(ctx context.Context, a *app.App) (*mirror.Snapshot, error) {
	repoKey := config.RepoKey(a.RepoRoot)
	slug := filepath.Base(a.RepoRoot)
	snap := &mirror.Snapshot{RepoKey: repoKey, Slug: slug}

	items, err := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			return nil, err
		}
		switch it.Kind {
		case workitem.KindTask:
			snap.Tasks = append(snap.Tasks, b)
		case workitem.KindExecution:
			snap.Executions = append(snap.Executions, b)
		default:
			snap.Stories = append(snap.Stories, b)
		}
	}

	// Provenance/source for process docs (workflows/skills/principles).
	prov, src := map[string]string{}, map[string]string{}
	if a.Store.DocIndex != nil {
		if rows, err := substrate.List(ctx, a.Store.DocIndex); err == nil {
			for _, r := range rows {
				key := r.Kind + "\x00" + r.Name
				prov[key] = r.Provenance
				src[key] = r.Source
			}
		}
		docs, err := a.Store.DocIndex.List(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			key := d.Kind + "\x00" + d.Name
			row := map[string]any{
				"name":       d.Name,
				"kind":       d.Kind,
				"body":       d.Body,
				"headline":   d.Headline,
				"path":       d.Path,
				"hash":       d.Hash,
				"size":       d.Size,
				"mod_time":   d.ModTime,
				"indexed_at": d.IndexedAt,
				"embedded":   d.Embedded,
				"provenance": prov[key],
				"source":     src[key],
			}
			b, _ := json.Marshal(row)
			snap.Docs = append(snap.Docs, b)
		}
	}

	if a.Store.Ledger != nil {
		if led, err := listLedgerJSON(ctx, a); err == nil {
			snap.LedgerEvents = led
		}
	}

	if a.Store.Leases != nil {
		if seats, err := listSeatsJSON(ctx, a); err == nil {
			snap.Seats = seats
		}
	}

	// Settings blob — display rows the RO settings page renders.
	if cfgBytes, err := marshalSettingsBlob(a); err == nil {
		snap.Settings = cfgBytes
	}

	// Per-partition identity/meta for footer + account strip.
	id := mirror.IdentityMeta{
		ProjectName: filepath.Base(a.RepoRoot),
		RepoRoot:    a.RepoRoot,
		FooterEmail: gitConfigEmail(a.RepoRoot),
	}
	if b, err := json.Marshal(id); err == nil {
		snap.Identity = b
	}

	// Story/task attachment docs (plans, step-summaries, …) under the runtime stories dir.
	if docs, err := listStoryDocsJSON(a); err == nil {
		snap.StoryDocs = docs
	}

	return snap, nil
}

// listStoryDocsJSON walks the runtime stories/ attachment tree and emits
// story_doc rows with id "storyID/name" for mirror detail pages.
func listStoryDocsJSON(a *app.App) ([]json.RawMessage, error) {
	storiesDir := filepath.Join(a.RuntimeDir, "stories")
	if a.RuntimeDir == "" {
		// Fall back to home-keyed runtime layout.
		rt := a.Config.ResolveRuntimeDir(a.RepoRoot)
		storiesDir = filepath.Join(rt.Dir, "stories")
	}
	ents, err := os.ReadDir(storiesDir)
	if err != nil {
		return nil, nil // no attachments yet
	}
	var out []json.RawMessage
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		storyID := e.Name()
		sub := filepath.Join(storiesDir, storyID)
		files, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			data, rerr := os.ReadFile(filepath.Join(sub, f.Name()))
			if rerr != nil {
				continue
			}
			name := strings.TrimSuffix(f.Name(), ".md")
			typ := ""
			// light frontmatter type: extract if present
			body := string(data)
			if strings.HasPrefix(body, "---\n") {
				if end := strings.Index(body[4:], "\n---"); end >= 0 {
					fm := body[4 : 4+end]
					for _, line := range strings.Split(fm, "\n") {
						if strings.HasPrefix(line, "type:") {
							typ = strings.TrimSpace(strings.TrimPrefix(line, "type:"))
							typ = strings.Trim(typ, `"'`)
						}
					}
				}
			}
			row := map[string]any{
				"id":       storyID + "/" + name,
				"story_id": storyID,
				"name":     name,
				"type":     typ,
				"body":     body,
			}
			b, _ := json.Marshal(row)
			out = append(out, b)
		}
	}
	return out, nil
}

func listLedgerJSON(ctx context.Context, a *app.App) ([]json.RawMessage, error) {
	if a.Store.Ledger == nil {
		return nil, nil
	}
	entries, err := a.Store.Ledger.ListAll(ctx, 10000)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func listSeatsJSON(ctx context.Context, a *app.App) ([]json.RawMessage, error) {
	if a.Store.Leases == nil {
		return nil, nil
	}
	_, _ = a.Store.Leases.Reap(ctx)
	all, err := a.Store.Leases.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]json.RawMessage, 0, len(all))
	for _, l := range all {
		row := map[string]any{
			"id":         l.ItemID,
			"kind":       l.Kind,
			"story_seat": l.StorySeat,
			"owner":      l.Owner,
			"state":      l.State,
			"in_flight":  l.InFlight,
			"stale":      lease.IsStale(l, now),
		}
		b, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func marshalSettingsBlob(a *app.App) (json.RawMessage, error) {
	// Push the resolved config as a flat display map the mirror settings page can
	// render without reopening the repo toml.
	type row struct {
		FieldID string `json:"field_id"`
		Label   string `json:"label"`
		Help    string `json:"help"`
		Value   string `json:"value"`
		Section string `json:"section"`
	}
	var rows []row
	for _, s := range config.Settings {
		rows = append(rows, row{
			FieldID: s.FieldID(),
			Label:   s.Label,
			Help:    s.Help,
			Value:   config.SettingDisplay(a.Config, s),
			Section: s.Section,
		})
	}
	return json.Marshal(map[string]any{
		"repo_root": a.RepoRoot,
		"rows":      rows,
	})
}

func gitConfigEmail(repoRoot string) string {
	cmd := exec.Command("git", "config", "--get", "user.email")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
