package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrMeFileMissing is returned when GET /api/v1/me/files/{path} yields 404.
var ErrMeFileMissing = errors.New("personal file not found — push the catalog first with `satelle agent profiles push`")

// MeFileItem is one entry in GET /api/v1/me/files (list).
type MeFileItem struct {
	Path       string `json:"path"`
	BlobSHA256 string `json:"blob_sha256,omitempty"`
	Size       int64  `json:"size,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// MeCatalogPath is the reserved personal-store path for the machine-wide
// agent profile catalog (~/.satelle/agents.toml). Other paths may be added
// later; the CLI only uses this one for sty_940938e3.
const MeCatalogPath = "agents.toml"

// meFilesRoute is GET/PUT collection root.
func meFilesRoute() string { return "/api/v1/me/files" }

// meFileRoute is the per-path route under the personal user store.
func meFileRoute(path string) string {
	path = strings.Trim(path, "/")
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return meFilesRoute() + "/" + strings.Join(segs, "/")
}

// ListMeFiles returns the personal user-store file list (GET /api/v1/me/files).
func (c *Client) ListMeFiles(ctx context.Context) ([]MeFileItem, error) {
	var out []MeFileItem
	if err := c.getJSON(ctx, meFilesRoute(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PushMeFile PUTs one file's verbatim bytes to /api/v1/me/files/{path}.
// A missing credential yields ErrLoginRequired. Created is true on HTTP 201.
func (c *Client) PushMeFile(ctx context.Context, path string, content []byte) (ConfigPushResult, error) {
	if content == nil {
		content = []byte{}
	}
	resp, err := c.doAuthed(ctx, http.MethodPut, meFileRoute(path), content, "text/plain; charset=utf-8")
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
				return ConfigPushResult{}, fmt.Errorf("hosted: decode me file push: %w", err)
			}
		}
		res.Path = path
		res.Created = resp.StatusCode == http.StatusCreated
		return res, nil
	case http.StatusUnauthorized:
		return ConfigPushResult{}, ErrLoginRequired
	default:
		return ConfigPushResult{}, fmt.Errorf("hosted: PUT me/files/%s: %s", path, serverError(resp.StatusCode, body))
	}
}

// MeFileContent GETs one path from the personal user store.
// 404 → ErrMeFileMissing; 401 → ErrLoginRequired.
func (c *Client) MeFileContent(ctx context.Context, path string) ([]byte, string, error) {
	resp, err := c.doAuthed(ctx, http.MethodGet, meFileRoute(path), nil, "")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return body, resp.Header.Get("ETag"), nil
	case http.StatusNotFound:
		return nil, "", ErrMeFileMissing
	case http.StatusUnauthorized:
		return nil, "", ErrLoginRequired
	default:
		return nil, "", fmt.Errorf("hosted: GET me/files/%s: %s", path, serverError(resp.StatusCode, body))
	}
}
