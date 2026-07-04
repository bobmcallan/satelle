package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrLoginRequired is returned when there is no usable credential — no stored
// tokens, or a refresh that failed. Surfaced to the user as a clear prompt to
// run `satelle login`, never a raw 401.
var ErrLoginRequired = errors.New("not signed in — run \"satelle login\"")

// ErrSlugConflict is returned when a project create collides with an existing
// slug (HTTP 409). Surfaced as a clear "slug already exists" message, never a
// raw response body.
var ErrSlugConflict = errors.New("project slug already exists on the server")

// Principal is the identity returned by GET /api/v1/me.
type Principal struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Project is a hosted project as returned by the projects API. Role is only
// populated by responses that carry the caller's membership role.
type Project struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
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

// ListProjects returns the caller's projects via GET /api/v1/projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	if err := c.getJSON(ctx, "/api/v1/projects", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateProject creates a project on the hosted server (POST /api/v1/projects),
// making the authenticated principal its owner. A slug already in use yields
// ErrSlugConflict (never the raw 409 body); a missing/expired credential yields
// ErrLoginRequired.
func (c *Client) CreateProject(ctx context.Context, slug, name string) (Project, error) {
	payload, err := json.Marshal(map[string]string{"slug": slug, "name": name})
	if err != nil {
		return Project{}, fmt.Errorf("hosted: encode project: %w", err)
	}
	resp, err := c.doAuthed(ctx, http.MethodPost, "/api/v1/projects", payload)
	if err != nil {
		return Project{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusCreated:
		var p Project
		if err := json.Unmarshal(body, &p); err != nil {
			return Project{}, fmt.Errorf("hosted: decode /api/v1/projects: %w", err)
		}
		return p, nil
	case http.StatusConflict:
		// A 409 is a slug collision — surface the clear sentinel, never the body.
		return Project{}, ErrSlugConflict
	case http.StatusUnauthorized:
		return Project{}, ErrLoginRequired
	default:
		return Project{}, fmt.Errorf("hosted: POST /api/v1/projects: %s", serverError(resp.StatusCode, body))
	}
}

// getJSON performs an authenticated GET of an /api/v1 path and decodes JSON.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.doAuthed(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hosted: GET %s: %s", path, serverError(resp.StatusCode, body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("hosted: decode %s: %w", path, err)
	}
	return nil
}

// serverError renders a concise "HTTP <code>: <message>" string, preferring a
// parsed JSON {"error"|"message"} field over the raw body so callers surface a
// clean message rather than a dump.
func serverError(code int, body []byte) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		if msg := strings.TrimSpace(e.Error); msg != "" {
			return fmt.Sprintf("HTTP %d: %s", code, msg)
		}
		if msg := strings.TrimSpace(e.Message); msg != "" {
			return fmt.Sprintf("HTTP %d: %s", code, msg)
		}
	}
	return fmt.Sprintf("HTTP %d", code)
}

// doAuthed sends the request (payload nil for a body-less GET) with the bearer
// token; on 401 it refreshes once (persisting the rotated pair before retrying)
// and resends. A missing credential or a failed refresh yields ErrLoginRequired.
func (c *Client) doAuthed(ctx context.Context, method, path string, payload []byte) (*http.Response, error) {
	cred, err := c.store.Load(c.server)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return nil, ErrLoginRequired
		}
		return nil, err
	}

	resp, err := c.send(ctx, method, path, cred.AccessToken, payload)
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
	// Carry the stored identity across rotation — credentialFromToken only knows
	// the token response, so without this the display_name/email would be wiped on
	// every refresh (~1h after login), sending the UI back to the legacy path.
	rotated.DisplayName, rotated.Email = cred.DisplayName, cred.Email
	if sErr := c.store.Save(rotated); sErr != nil {
		return nil, fmt.Errorf("hosted: persist refreshed credential: %w", sErr)
	}
	return c.send(ctx, method, path, rotated.AccessToken, payload)
}

// send builds and sends one authenticated request. payload (nil for none) is
// rebuilt into a fresh reader on EVERY call so the 401→refresh retry resends the
// full body — a one-shot io.Reader would be drained by the first attempt.
func (c *Client) send(ctx context.Context, method, path, accessToken string, payload []byte) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hosted: %s %s: %w", method, path, err)
	}
	return resp, nil
}
