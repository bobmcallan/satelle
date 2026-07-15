package hosted

// Workspace-scoped work-state mirror client (epic:scoped-sync, order:7;
// epic:workspace-rehydrate, order:3). POST ingest pushes local→server; GET
// items/ledger pull for rehydrate. The server stamps origin=cli-sync itself —
// the client never supplies it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// WorkstateItem is one listed mirror row: promoted fields plus the CLI record
// verbatim in Record (the payload the client pushed).
type WorkstateItem struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Type   string          `json:"type"`
	Status string          `json:"status"`
	Title  string          `json:"title"`
	Origin string          `json:"origin"`
	Record json.RawMessage `json:"record"`
}

// WorkstateLedgerRow is one listed ledger mirror row.
type WorkstateLedgerRow struct {
	ID      string          `json:"id"`
	StoryID string          `json:"story_id"`
	Kind    string          `json:"kind"`
	Type    string          `json:"type"`
	Origin  string          `json:"origin"`
	Record  json.RawMessage `json:"record"`
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

// ListWorkstateItems GETs the project's mirrored work-state items
// (.../workstate/items?project=). kind filters when non-empty (story|execution).
func (c *Client) ListWorkstateItems(ctx context.Context, wsID, project, kind string) ([]WorkstateItem, error) {
	route := withProjectQuery(workstateRoute(wsID)+"/items", project)
	if k := strings.TrimSpace(kind); k != "" {
		route += "&kind=" + url.QueryEscape(k)
	}
	resp, err := c.doAuthed(ctx, http.MethodGet, route, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		var out []WorkstateItem
		if len(body) > 0 {
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, fmt.Errorf("hosted: decode workstate items: %w", err)
			}
		}
		if out == nil {
			out = []WorkstateItem{}
		}
		return out, nil
	case http.StatusUnauthorized:
		return nil, ErrLoginRequired
	default:
		return nil, fmt.Errorf("hosted: GET workstate items: %s", serverError(resp.StatusCode, body))
	}
}

// ListWorkstateLedger GETs the project's mirrored ledger
// (.../workstate/ledger?project=). The whole project ledger is returned when
// storyID is empty.
func (c *Client) ListWorkstateLedger(ctx context.Context, wsID, project, storyID string) ([]WorkstateLedgerRow, error) {
	route := withProjectQuery(workstateRoute(wsID)+"/ledger", project)
	if s := strings.TrimSpace(storyID); s != "" {
		route += "&story=" + url.QueryEscape(s)
	}
	resp, err := c.doAuthed(ctx, http.MethodGet, route, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		var out []WorkstateLedgerRow
		if len(body) > 0 {
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, fmt.Errorf("hosted: decode workstate ledger: %w", err)
			}
		}
		if out == nil {
			out = []WorkstateLedgerRow{}
		}
		return out, nil
	case http.StatusUnauthorized:
		return nil, ErrLoginRequired
	default:
		return nil, fmt.Errorf("hosted: GET workstate ledger: %s", serverError(resp.StatusCode, body))
	}
}
