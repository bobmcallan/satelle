package hosted

// Workspace-scoped work-state mirror client (epic:scoped-sync, order:7).
// One-way local→server only: POST an ingest batch; nothing pulls work state
// back. The server stamps origin=cli-sync itself — the client never supplies it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// WorkstateIngest is the POST body for .../workstate: opaque CLI records the
// server promotes a few fields from and stores whole as payload.
type WorkstateIngest struct {
	Items  []json.RawMessage `json:"items"`
	Ledger []json.RawMessage `json:"ledger"`
}

// WorkstateIngestResult is the POST response: counts of upserted rows.
type WorkstateIngestResult struct {
	Items  int `json:"items"`
	Ledger int `json:"ledger"`
}

// workstateRoute is the workspace work-state ingest endpoint.
func workstateRoute(wsID string) string {
	return "/api/v1/workspaces/" + url.PathEscape(wsID) + "/workstate"
}

// PushWorkstate POSTs a one-way work-state batch into the project's partition of
// the workspace mirror (.../workstate?project=). Always targets the workspace
// the caller passes — the CLI always passes the personal workspace (work-state
// never team-shares). A missing credential yields ErrLoginRequired; other
// non-200s a clean serverError.
func (c *Client) PushWorkstate(ctx context.Context, wsID, project string, batch WorkstateIngest) (WorkstateIngestResult, error) {
	if batch.Items == nil {
		batch.Items = []json.RawMessage{}
	}
	if batch.Ledger == nil {
		batch.Ledger = []json.RawMessage{}
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return WorkstateIngestResult{}, fmt.Errorf("hosted: encode workstate: %w", err)
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, withProjectQuery(workstateRoute(wsID), project), payload, contentJSON)
	if err != nil {
		return WorkstateIngestResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		var res WorkstateIngestResult
		if len(body) > 0 {
			if err := json.Unmarshal(body, &res); err != nil {
				return WorkstateIngestResult{}, fmt.Errorf("hosted: decode workstate: %w", err)
			}
		}
		return res, nil
	case http.StatusUnauthorized:
		return WorkstateIngestResult{}, ErrLoginRequired
	default:
		return WorkstateIngestResult{}, fmt.Errorf("hosted: POST workstate: %s", serverError(resp.StatusCode, body))
	}
}
