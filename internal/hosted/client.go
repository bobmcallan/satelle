package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

// Workspace is a hosted workspace as returned by the workspaces API. Kind is
// "personal" (the caller's own workspace) or "team" (a shared/team workspace).
// The server returns no slug; clients identify a workspace by name or id.
type Workspace struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
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

// Workspaces returns the caller's workspaces via GET /api/v1/workspaces. A
// missing/expired credential yields ErrLoginRequired (transparent refresh aside);
// any other non-200 yields a clean serverError, never a raw body.
func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	var out []Workspace
	if err := c.getJSON(ctx, "/api/v1/workspaces", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// personalWorkspaceName is the scoped-sync personal sentinel: the active-
// workspace name that resolves to the caller's own (kind=personal) workspace.
// It mirrors config.PersonalWorkspace without importing config.
const personalWorkspaceName = "personal"

// ErrConfigFileMissing is returned when a config path (or a pinned version of
// it) does not exist in the workspace. Deploy treats it as "skip this file" when
// pinning a version that predates a file's first appearance.
var ErrConfigFileMissing = errors.New("config file not found in workspace")

// ConfigItem is one entry in a workspace's config manifest — the deploy set for
// "set up project X like Y". Mirrors api.ConfigItem (the published shape); the
// internal client owns its type independently (no api import).
type ConfigItem struct {
	Path       string `json:"path"`
	Version    int    `json:"version"`
	BlobSHA256 string `json:"blob_sha256"`
	Size       int64  `json:"size"`
	CreatedAt  string `json:"created_at"`
}

// ConfigPushResult is the per-file response to PUT .../config/{path}. Created is
// true when this push appended a new head (HTTP 201) and false when it was an
// idempotent re-push of the current head (HTTP 200).
type ConfigPushResult struct {
	Path       string `json:"path"`
	Version    int    `json:"version"`
	BlobSHA256 string `json:"blob_sha256"`
	Size       int64  `json:"size"`
	Created    bool   `json:"created"`
}

// ActiveWorkspaceID resolves the active workspace NAME to its server ID via
// GET /api/v1/workspaces — the order:5 name→id bridge every workspace-scoped
// config route keys on (the config carries the handle by NAME; routes need the
// id). The personal sentinel (or an empty name) resolves to the caller's own
// workspace (kind=personal); any other name to its exact-name match. A name with
// no match yields a clear error listing the available workspaces, never a body.
func (c *Client) ActiveWorkspaceID(ctx context.Context, name string) (string, error) {
	workspaces, err := c.Workspaces(ctx)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(name)
	if target == "" || target == personalWorkspaceName {
		for _, ws := range workspaces {
			if ws.Kind == personalWorkspaceName {
				return ws.ID, nil
			}
		}
		return "", fmt.Errorf("no personal workspace found for %s", c.server)
	}
	for _, ws := range workspaces {
		if ws.Name == target {
			return ws.ID, nil
		}
	}
	return "", workspaceIDNotFound(target, workspaces)
}

// workspaceIDNotFound builds the clear error for an unresolvable workspace name,
// listing the available workspaces as "name (kind)" — built from the parsed
// list, never the raw body, so a response detail cannot leak.
func workspaceIDNotFound(name string, workspaces []Workspace) error {
	if len(workspaces) == 0 {
		return fmt.Errorf("workspace %q not found — the account has no workspaces", name)
	}
	parts := make([]string, len(workspaces))
	for i, ws := range workspaces {
		parts[i] = fmt.Sprintf("%s (%s)", ws.Name, ws.Kind)
	}
	return fmt.Errorf("workspace %q not found — available: %s", name, strings.Join(parts, ", "))
}

// configManifestRoute is the project-addressed config root (manifest / deploy
// set). project is an id or slug (sty_ca64d0cb — team-workspaces Order 2).
func configManifestRoute(project string) string {
	return "/api/v1/projects/" + url.PathEscape(project) + "/config"
}

// configFileRoute is the per-file route, escaping each path segment so a path
// with odd characters round-trips while the slashes that structure it survive.
func configFileRoute(project, path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return configManifestRoute(project) + "/" + strings.Join(segs, "/")
}

// ConfigManifest returns a project's deploy set (the latest version of every
// config path) via GET /api/v1/projects/{project}/config.
func (c *Client) ConfigManifest(ctx context.Context, project string) ([]ConfigItem, error) {
	var out []ConfigItem
	if err := c.getJSON(ctx, configManifestRoute(project), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PushConfigFile PUTs one file's verbatim bytes to
// /api/v1/projects/{project}/config/{path}, appending a new version (head) when
// the content differs from the current head. The returned Created flag
// distinguishes a new head (HTTP 201) from an idempotent re-push of the current
// head (HTTP 200). A missing credential yields ErrLoginRequired; a body-bearing
// retry survives the 401→refresh path.
func (c *Client) PushConfigFile(ctx context.Context, project, path string, content []byte) (ConfigPushResult, error) {
	if content == nil {
		content = []byte{}
	}
	resp, err := c.doAuthed(ctx, http.MethodPut, configFileRoute(project, path), content, "text/plain; charset=utf-8")
	if err != nil {
		return ConfigPushResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var res ConfigPushResult
		if len(body) > 0 {
			if err := json.Unmarshal(body, &res); err != nil {
				return ConfigPushResult{}, fmt.Errorf("hosted: decode config push: %w", err)
			}
		}
		res.Path = path
		res.Created = resp.StatusCode == http.StatusCreated
		return res, nil
	case http.StatusUnauthorized:
		return ConfigPushResult{}, ErrLoginRequired
	default:
		return ConfigPushResult{}, fmt.Errorf("hosted: PUT config %s: %s", path, serverError(resp.StatusCode, body))
	}
}

// ConfigFileContent GETs one path's content from the project — the latest head,
// or ?version=N for a pinned per-file version (0/omitted means latest). The
// returned etag is the blob sha256 the server sends in the ETag header. A 404
// (no such path, or the pinned version predates the file's first appearance)
// yields ErrConfigFileMissing.
func (c *Client) ConfigFileContent(ctx context.Context, project, path string, version int) ([]byte, string, error) {
	route := configFileRoute(project, path)
	if version > 0 {
		route += "?version=" + strconv.Itoa(version)
	}
	resp, err := c.doAuthed(ctx, http.MethodGet, route, nil, "")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return body, resp.Header.Get("ETag"), nil
	case http.StatusNotFound:
		return nil, "", ErrConfigFileMissing
	case http.StatusUnauthorized:
		return nil, "", ErrLoginRequired
	default:
		return nil, "", fmt.Errorf("hosted: GET config %s: %s", path, serverError(resp.StatusCode, body))
	}
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
	resp, err := c.doAuthed(ctx, http.MethodPost, "/api/v1/projects", payload, contentJSON)
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
	resp, err := c.doAuthed(ctx, http.MethodGet, path, nil, "")
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
// and resends. contentType is set on body-bearing requests ("" leaves it unset,
// for GETs); contentJSON is the common JSON case. A missing credential or a
// failed refresh yields ErrLoginRequired.
func (c *Client) doAuthed(ctx context.Context, method, path string, payload []byte, contentType string) (*http.Response, error) {
	cred, err := c.store.Load(c.server)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return nil, ErrLoginRequired
		}
		return nil, err
	}

	resp, err := c.send(ctx, method, path, cred.AccessToken, payload, contentType)
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
	return c.send(ctx, method, path, rotated.AccessToken, payload, contentType)
}

// contentJSON is the Content-Type for JSON request bodies.
const contentJSON = "application/json"

// send builds and sends one authenticated request. payload (nil for none) is
// rebuilt into a fresh reader on EVERY call so the 401→refresh retry resends the
// full body — a one-shot io.Reader would be drained by the first attempt.
// contentType is set only when both payload and contentType are non-empty (a raw
// config-file PUT carries bytes, not JSON).
func (c *Client) send(ctx context.Context, method, path, accessToken string, payload []byte, contentType string) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if payload != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hosted: %s %s: %w", method, path, err)
	}
	return resp, nil
}
