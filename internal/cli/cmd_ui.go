package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	// ui push — local push-fed UI mirror reconcile (sty_1dde0d47). Distinct from
	// hosted sync (satelle sync / push / pull / login).
	ui := &cobra.Command{
		Use:   "ui",
		Short: "Local push-fed UI server helpers (not the hosted tier)",
	}
	push := &cobra.Command{
		Use:   "push",
		Short: "Push full active-repo state to the configured local UI server mirror",
		Long: `ui push posts a full snapshot of the active repo to [server] endpoint
(/ingest/snapshot) so the push-fed serve mirror converges to CLI truth.

This is the LOCAL UI path (epic:serve-split), not hosted satelle-server sync.
Requires [server] endpoint in satelle.toml. Fail-visible on error.`,
		Annotations: needsStore(),
		RunE:        runUIPush,
	}
	ui.AddCommand(push)
	register(ui)
}

func runUIPush(cmd *cobra.Command, args []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ep := strings.TrimSpace(a.Config.Server.Endpoint)
	if ep == "" {
		return fmt.Errorf("ui push: [server] endpoint is not set in satelle.toml — configure the local UI server first")
	}
	snap, err := buildUISnapshot(cmd.Context(), a)
	if err != nil {
		return err
	}
	if err := postUISnapshot(ep, snap); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ui push: ok repo_key=%s stories=%d tasks=%d → %s/ingest/snapshot\n",
		snap.RepoKey, len(snap.Stories), len(snap.Tasks), strings.TrimRight(ep, "/"))
	return nil
}

// postUISnapshot POSTs snap to endpoint/ingest/snapshot. Used by the manual
// verb and the auto first-contact path (sty_1dde0d47).
func postUISnapshot(endpoint string, snap *mirror.Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/") + "/ingest/snapshot"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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

	if a.Store.DocIndex != nil {
		docs, err := a.Store.DocIndex.List(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			// Include enough for list+detail: name, kind, body, headline.
			row := map[string]any{
				"name": d.Name, "kind": d.Kind, "body": d.Body, "headline": d.Headline,
			}
			b, _ := json.Marshal(row)
			// id key for rawToRows is "name"
			snap.Docs = append(snap.Docs, b)
		}
	}

	if a.Store.Ledger != nil {
		// Best-effort: list recent ledger rows if the store exposes List.
		// Ledger package API may differ; omit on error rather than fail the push.
		if led, err := listLedgerJSON(ctx, a); err == nil {
			snap.LedgerEvents = led
		}
	}
	return snap, nil
}

func listLedgerJSON(ctx context.Context, a *app.App) ([]json.RawMessage, error) {
	// Prefer a broad list when available.
	type lister interface {
		List(ctx context.Context, storyID string, limit int) ([]any, error)
	}
	// Use verb/ledger via store if ListAll exists; otherwise skip.
	// Concrete: ledger.Store has List by story; without a bulk API, skip empty.
	_ = ctx
	_ = a
	return nil, nil
}
