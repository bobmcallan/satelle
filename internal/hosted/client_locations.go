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
	if c.location == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"id": c.location, "label": label})
	if err != nil {
		return fmt.Errorf("hosted: encode location register: %w", err)
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, "/api/v1/locations", payload, contentJSON)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusUnauthorized:
		return ErrLoginRequired
	default:
		return fmt.Errorf("hosted: POST /api/v1/locations: %s", serverError(resp.StatusCode, body))
	}
}

// EnsureLocationRegistered registers c's location for repoRoot on server once.
// A successful register is persisted so later calls short-circuit. The caller
// treats non-ErrLoginRequired failures as non-fatal.
func EnsureLocationRegistered(ctx context.Context, c *Client, repoRoot, server string) error {
	if c == nil || c.location == "" {
		return nil
	}
	if LocationRegistered(repoRoot, server) {
		return nil
	}
	if err := c.RegisterLocation(ctx, LocationLabel(repoRoot)); err != nil {
		return err
	}
	return MarkLocationRegistered(repoRoot, server)
}
