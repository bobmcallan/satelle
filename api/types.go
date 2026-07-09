// Package api provides reference wire types for the satelle hosted API.
//
// Servers in any language implement these shapes; the OpenAPI spec
// (openapi.yaml) is the canonical interface definition. This package is a
// standalone reference — it does not import internal/hosted, and the
// internal client types are structurally identical but independently owned.
package api

import "encoding/json"

// Principal is the identity returned by GET /api/v1/me.
type Principal struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Project is a hosted project as returned by the projects API.
// Role is only populated by responses that carry the caller's membership.
type Project struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// Workspace is a hosted workspace as returned by the workspaces API. Kind is
// "personal" (the caller's own workspace) or "team" (a shared/team workspace).
// The server returns no slug; clients identify a workspace by name or id.
type Workspace struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// CreateProjectRequest is the JSON body for POST /api/v1/projects.
type CreateProjectRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// TokenResponse is the /oauth/token success body (authorization_code and
// refresh_token grants).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// OAuthError is the /oauth/token error body ({"error","error_description"}).
type OAuthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

// ErrorEnvelope is the standard JSON error body returned by API endpoints.
type ErrorEnvelope struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ConfigItem is one entry in a workspace's config manifest — the deploy set for
// "set up project X like project Y". It is the latest head of a config path in a
// workspace, returned by GET /api/v1/workspaces/{id}/config.
type ConfigItem struct {
	Path       string `json:"path"`
	Version    int    `json:"version"`
	BlobSHA256 string `json:"blob_sha256"`
	Size       int64  `json:"size"`
	CreatedAt  string `json:"created_at"`
}

// ConfigPushResult is the per-file response to PUT
// /api/v1/workspaces/{id}/config/{path}. Created is true when the push appended a
// new head (HTTP 201) and false when it was an idempotent re-push of the current
// head (HTTP 200) — the per-file idempotency signal.
type ConfigPushResult struct {
	Path       string `json:"path"`
	Version    int    `json:"version"`
	BlobSHA256 string `json:"blob_sha256"`
	Size       int64  `json:"size"`
	Created    bool   `json:"created"`
}

// ConfigRollbackRequest is the body for POST
// /api/v1/workspaces/{id}/config-rollback: append a new head copying an earlier
// version of a path (recovery of a prior version).
type ConfigRollbackRequest struct {
	Path    string `json:"path"`
	Version int    `json:"version"`
}

// DocumentChanges is the response to GET /api/v1/workspaces/{id}/documents
// (optionally ?since=<cursor>): the items that changed after the caller's
// cursor, plus the new cursor for the next incremental pull. Item shape reuses
// ConfigItem (path/version/blob_sha256/size/created_at) — same per-file head
// metadata as the config store.
type DocumentChanges struct {
	Items  []ConfigItem `json:"items"`
	Cursor string       `json:"cursor"`
}

// WorkstateIngest is the POST body for /api/v1/workspaces/{id}/workstate —
// a one-way local→server batch of stories/executions (items) and ledger entries.
// The server stamps origin=cli-sync; the client never supplies origin.
type WorkstateIngest struct {
	Items  []json.RawMessage `json:"items"`
	Ledger []json.RawMessage `json:"ledger"`
}

// WorkstateIngestResult is the POST response: counts of upserted rows.
type WorkstateIngestResult struct {
	Items  int `json:"items"`
	Ledger int `json:"ledger"`
}
