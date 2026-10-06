package hosted

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// RegisterLocation POSTs this client's location id to /api/v1/locations.
// Idempotent on the server (200 or 201). Empty location is a no-op.
func (c *Client) RegisterLocation(ctx context.Context, label string) error {
	_, err := c.RegisterLocationPrincipal(ctx, label)
	return err
}

// RegisterLocationPrincipal is RegisterLocation that also returns the user id
// the server reports for the caller ("" when the response carries none, or when
// there is no location to register). It is how a session token learns who it
// acts as without GET /api/v1/me, which a session token is refused on.
func (c *Client) RegisterLocationPrincipal(ctx context.Context, label string) (string, error) {
	if c.location == "" {
		return "", nil
	}
	payload, err := json.Marshal(map[string]string{"id": c.location, "label": label})
	if err != nil {
		return "", fmt.Errorf("hosted: encode location register: %w", err)
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, "/api/v1/locations", payload, contentJSON)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var out struct {
			UserID string `json:"user_id"`
		}
		_ = json.Unmarshal(body, &out)
		return out.UserID, nil
	case http.StatusUnauthorized:
		return "", ErrLoginRequired
	case http.StatusNotFound:
		if scope := c.sessionScopeErr(); scope != nil {
			return "", scope
		}
		return "", fmt.Errorf("hosted: POST /api/v1/locations: %s", serverError(resp.StatusCode, body))
	default:
		return "", fmt.Errorf("hosted: POST /api/v1/locations: %s", serverError(resp.StatusCode, body))
	}
}

// EnsureLocationRegistered registers c's location for repoRoot on server once.
// A successful register is persisted so later calls short-circuit. The caller
// treats non-ErrLoginRequired failures as non-fatal.
//
// Under a session token the registration also yields the principal id, so it is
// repeated until one is cached for this token: the registered-once shortcut
// would otherwise leave a fresh token's user unresolved.
func EnsureLocationRegistered(ctx context.Context, c *Client, repoRoot, server string) error {
	if c == nil || c.location == "" {
		return nil
	}
	if cred, err := c.store.Load(c.server); err == nil && cred.IsSession() {
		if _, known := SessionPrincipal(cred); known && LocationRegistered(repoRoot, server) {
			return nil
		}
		if _, err := ResolveSessionPrincipal(ctx, c, repoRoot, server); err != nil {
			return err
		}
		return MarkLocationRegistered(repoRoot, server)
	}
	if LocationRegistered(repoRoot, server) {
		return nil
	}
	if err := c.RegisterLocation(ctx, LocationLabel(repoRoot)); err != nil {
		return err
	}
	return MarkLocationRegistered(repoRoot, server)
}
