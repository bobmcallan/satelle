package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrLoginRequired is returned when there is no usable credential — no stored
// tokens, or a refresh that failed. Surfaced to the user as a clear prompt to
// run `satelle login`, never a raw 401.
var ErrLoginRequired = errors.New("not signed in — run \"satelle login\"")

// Principal is the identity returned by GET /api/v1/me.
type Principal struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Client is an authenticated HTTP client for a hosted server's /api/v1/*
// surface. It attaches the stored bearer access token and, on a 401,
// transparently refreshes (persisting the ROTATED refresh token before it
// retries the request once).
type Client struct {
	server string
	store  Store
	http   *http.Client
}

// NewClient builds a client for server, backed by store for token persistence.
// A nil httpClient uses a 30s-timeout default.
func NewClient(server string, store Store, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{server: normalizeServerURL(server), store: store, http: httpClient}
}

// Me resolves the current principal via GET /api/v1/me.
func (c *Client) Me(ctx context.Context) (Principal, error) {
	var p Principal
	if err := c.getJSON(ctx, "/api/v1/me", &p); err != nil {
		return Principal{}, err
	}
	return p, nil
}

// getJSON performs an authenticated GET of an /api/v1 path and decodes JSON.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.doAuthed(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hosted: GET %s: HTTP %d: %s", path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("hosted: decode %s: %w", path, err)
	}
	return nil
}

// doAuthed sends the request with the bearer token; on 401 it refreshes once
// (persisting the rotated pair before retrying) and resends. A missing
// credential or a failed refresh yields ErrLoginRequired.
func (c *Client) doAuthed(ctx context.Context, method, path string) (*http.Response, error) {
	cred, err := c.store.Load(c.server)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return nil, ErrLoginRequired
		}
		return nil, err
	}

	resp, err := c.send(ctx, method, path, cred.AccessToken)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	// Access token rejected — refresh, PERSIST the rotated refresh immediately
	// (the old one dies on rotation; a late persist would strand a dead token on
	// crash), then retry the request exactly once.
	resp.Body.Close()
	tok, rErr := refreshGrant(ctx, c.http, c.server, cred.RefreshToken)
	if rErr != nil {
		return nil, fmt.Errorf("%w (refresh failed: %v)", ErrLoginRequired, rErr)
	}
	rotated := credentialFromToken(c.server, tok)
	if sErr := c.store.Save(rotated); sErr != nil {
		return nil, fmt.Errorf("hosted: persist refreshed credential: %w", sErr)
	}
	return c.send(ctx, method, path, rotated.AccessToken)
}

func (c *Client) send(ctx context.Context, method, path, accessToken string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hosted: %s %s: %w", method, path, err)
	}
	return resp, nil
}
