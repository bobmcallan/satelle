package hosted

// The satelle.dev secrets and remote-credential routes the owner's CLI login
// uses (sty_0b1c72a9). A value goes up exactly once, in PutSecret, and no route
// here returns one: SecretMeta has no field that could hold it. The refusals of
// every call are the server's own, as an *APIError.
//
//	PUT    /api/v1/secrets/{key}                                  {value, expires_at?}   (personal)
//	PUT    /api/v1/workspaces/{ws}/secrets/{key}                  {value, expires_at?}   (team)
//	GET    /api/v1/secrets | /api/v1/workspaces/{ws}/secrets      [{key, scope, has_value, readers, ...}]
//	PUT    …/secrets/{key}/grants/{remote_id}                     (both scopes; 204)
//	PUT    /api/v1/remotes/{id}/key                               {public_key} → {fingerprint, set_at}
//	PUT    /api/v1/remotes/{id}/identity                          {issuer, subject}
//	DELETE /api/v1/remotes/{id}/identity
//	POST   /api/v1/remotes/{id}/runs                              → {run_id, expires_at}  (an owner-started run)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Scopes a secret is stored under on the server.
const (
	ScopePersonal = "personal"
	ScopeTeam     = "team"
)

// SecretMeta is what the server tells a reader about a secret: never its value.
// ExpiresAt and Expired are the lifetime the pusher declared.
type SecretMeta struct {
	Key       string   `json:"key"`
	Scope     string   `json:"scope"`
	HasValue  bool     `json:"has_value"`
	Readers   []string `json:"readers"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Expired   bool     `json:"expired,omitempty"`
}

// Expiry is the secret's expiry as a time, when the server reports one.
func (m SecretMeta) Expiry() (time.Time, bool) {
	if m.ExpiresAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, m.ExpiresAt)
	return t, err == nil
}

// secretsRoot is the collection a scope's secrets live under. A team secret
// names its workspace.
func secretsRoot(scope, workspace string) (string, error) {
	switch scope {
	case ScopePersonal:
		return "/api/v1/secrets", nil
	case ScopeTeam:
		if workspace == "" {
			return "", errors.New("hosted: a team secret needs its workspace")
		}
		return "/api/v1/workspaces/" + url.PathEscape(workspace) + "/secrets", nil
	}
	return "", fmt.Errorf("hosted: %q is not a secret scope", scope)
}

// PutSecret sets or rotates one secret under its scope. expiresAt, when not zero,
// is the lifetime the pusher declares for it. The value is sent and never echoed.
func (c *Client) PutSecret(ctx context.Context, scope, workspace, key, value string, expiresAt time.Time) error {
	root, err := secretsRoot(scope, workspace)
	if err != nil {
		return err
	}
	body := map[string]string{"value": value}
	if !expiresAt.IsZero() {
		body["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	_, err = c.authedCall(ctx, http.MethodPut, root+"/"+url.PathEscape(key), body)
	return err
}

// ListSecrets returns the secrets of one scope by name and attributes. The server
// returns no value.
func (c *Client) ListSecrets(ctx context.Context, scope, workspace string) ([]SecretMeta, error) {
	root, err := secretsRoot(scope, workspace)
	if err != nil {
		return nil, err
	}
	raw, err := c.authedCall(ctx, http.MethodGet, root, nil)
	if err != nil {
		return nil, err
	}
	var out []SecretMeta
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("hosted: decode secrets: %w", err)
	}
	for i := range out {
		if out[i].Scope == "" {
			out[i].Scope = scope
		}
	}
	return out, nil
}

// GrantSecret lets the remote read key.
func (c *Client) GrantSecret(ctx context.Context, scope, workspace, key, remoteID string) error {
	root, err := secretsRoot(scope, workspace)
	if err != nil {
		return err
	}
	_, err = c.authedCall(ctx, http.MethodPut, root+"/"+url.PathEscape(key)+"/grants/"+url.PathEscape(remoteID), nil)
	return err
}

// subRoute is the route of a part of a remote (its key, identity or runs). The tail
// is required, so no call built from it can address the remote itself: retiring a
// remote is RetireRemote's alone.
func subRoute(remoteID, tail string) string {
	if tail == "" || tail[0] != '/' {
		panic("hosted: a remote's sub-resource route needs a /tail")
	}
	return remoteRoute(remoteID, tail)
}

// RemoteKeyInfo is what registering a key reports: never the key.
type RemoteKeyInfo struct {
	Fingerprint string `json:"fingerprint"`
	SetAt       string `json:"set_at"`
}

// SetRemoteKey registers or rotates the remote's Ed25519 public key (publicKey is
// the standard base64 of its 32 raw bytes). The old key stops verifying at once.
func (c *Client) SetRemoteKey(ctx context.Context, remoteID, publicKey string) (RemoteKeyInfo, error) {
	raw, err := c.authedCall(ctx, http.MethodPut, subRoute(remoteID, "/key"), map[string]string{"public_key": publicKey})
	if err != nil {
		return RemoteKeyInfo{}, err
	}
	var info RemoteKeyInfo
	_ = json.Unmarshal(raw, &info)
	return info, nil
}

// SetRemoteIdentity binds the remote to a platform workload identity, the
// alternative to a key.
func (c *Client) SetRemoteIdentity(ctx context.Context, remoteID, issuer, subject string) error {
	_, err := c.authedCall(ctx, http.MethodPut, subRoute(remoteID, "/identity"), map[string]string{"issuer": issuer, "subject": subject})
	return err
}

// DeleteRemoteIdentity removes the remote's identity binding.
func (c *Client) DeleteRemoteIdentity(ctx context.Context, remoteID string) error {
	_, err := c.authedCall(ctx, http.MethodDelete, subRoute(remoteID, "/identity"), nil)
	return err
}

// OwnerRun is a run the remote's owner registered for a local-started node.
type OwnerRun struct {
	RunID     string `json:"run_id"`
	ExpiresAt string `json:"expires_at"`
}

// RefreshOwnerRun keeps an owner-registered run alive for another 30 minutes, under
// the owner's login.
func (c *Client) RefreshOwnerRun(ctx context.Context, remoteID, runID string) error {
	_, err := c.authedCall(ctx, http.MethodPost, subRoute(remoteID, "/runs/"+url.PathEscape(runID)+"/refresh"), struct{}{})
	return err
}

// EndOwnerRun ends an owner-registered run: no read is answered for it afterwards.
func (c *Client) EndOwnerRun(ctx context.Context, remoteID, runID string) error {
	_, err := c.authedCall(ctx, http.MethodDelete, subRoute(remoteID, "/runs/"+url.PathEscape(runID)), nil)
	return err
}

// CreateRun registers one run of a local remote, under the owner's login, for
// the node to read its granted secrets under. It wakes nothing.
func (c *Client) CreateRun(ctx context.Context, remoteID string) (OwnerRun, error) {
	raw, err := c.authedCall(ctx, http.MethodPost, subRoute(remoteID, "/runs"), struct{}{})
	if err != nil {
		return OwnerRun{}, err
	}
	var run OwnerRun
	if err := json.Unmarshal(raw, &run); err != nil || run.RunID == "" {
		return OwnerRun{}, errors.New("hosted: the server returned no run id")
	}
	return run, nil
}
