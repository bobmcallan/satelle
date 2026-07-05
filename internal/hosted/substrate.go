package hosted

// Substrate backup client (sty_f2c1899a): the sending end of a repo's authored
// .satelle/ backup. It targets the hosted server's project-scoped substrate API
// (satelle-server sty_caaca515): a push REPLACES the project's stored snapshot
// (files absent from the push are removed server-side), and a full pull returns
// every stored file with its verbatim bytes for a byte-exact restore. Content
// rides base64 in both directions because JSON strings cannot carry arbitrary
// bytes (CRLF, non-UTF-8) verbatim. These reuse the client's authed transport
// (bearer + 401→refresh→retry), so they inherit the same credential handling as
// the projects API.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// SubstrateFile is one authored .satelle/ file for a backup push or restore.
// Path is relative to .satelle/ (forward slashes, no leading .satelle/); Content
// is the verbatim bytes. Kind is optional promoted metadata (the top-level
// substrate dir), carried for the manifest and never required for a round-trip.
type SubstrateFile struct {
	Path    string
	Kind    string
	Content []byte
}

// SubstratePushSummary reports what a push changed, so idempotence is observable:
// an identical re-push returns all Unchanged with zero Added/Updated/Removed.
type SubstratePushSummary struct {
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Removed   int `json:"removed"`
}

// substrateFileWire is one file on the wire, symmetric across push/pull. Content
// is base64. On push only path/kind/content are sent; the server computes sha256
// and size (a client hash is never trusted), so those are read-only on pull.
type substrateFileWire struct {
	Path      string `json:"path"`
	Kind      string `json:"kind,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Content   string `json:"content,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// substratePath builds the project-scoped substrate endpoint, escaping the slug.
func substratePath(slug string) string {
	return "/api/v1/projects/" + url.PathEscape(slug) + "/substrate"
}

// PushSubstrate replaces project slug's stored .satelle/ snapshot with files
// (PUT /api/v1/projects/{slug}/substrate). Editor/owner role required. An
// identical re-push is idempotent — the server writes nothing and reports all
// Unchanged. A missing/expired credential yields ErrLoginRequired.
func (c *Client) PushSubstrate(ctx context.Context, slug string, files []SubstrateFile) (SubstratePushSummary, error) {
	wires := make([]substrateFileWire, 0, len(files))
	for _, f := range files {
		wires = append(wires, substrateFileWire{
			Path:    f.Path,
			Kind:    f.Kind,
			Content: base64.StdEncoding.EncodeToString(f.Content),
		})
	}
	payload, err := json.Marshal(map[string]any{"files": wires})
	if err != nil {
		return SubstratePushSummary{}, fmt.Errorf("hosted: encode substrate push: %w", err)
	}
	resp, err := c.doAuthed(ctx, http.MethodPut, substratePath(slug), payload)
	if err != nil {
		return SubstratePushSummary{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		var sum SubstratePushSummary
		if err := json.Unmarshal(body, &sum); err != nil {
			return SubstratePushSummary{}, fmt.Errorf("hosted: decode substrate push: %w", err)
		}
		return sum, nil
	case http.StatusUnauthorized:
		return SubstratePushSummary{}, ErrLoginRequired
	case http.StatusForbidden:
		return SubstratePushSummary{}, fmt.Errorf("hosted: substrate push: editor or owner role required on project %q", slug)
	case http.StatusNotFound:
		return SubstratePushSummary{}, fmt.Errorf("hosted: substrate push: project %q not found (or you are not a member)", slug)
	default:
		return SubstratePushSummary{}, fmt.Errorf("hosted: substrate push: %s", serverError(resp.StatusCode, body))
	}
}

// PullSubstrate returns project slug's entire stored snapshot WITH verbatim bytes
// (GET /api/v1/projects/{slug}/substrate?full=1) — the one-request restore source.
// A project with no backup yet returns an empty slice, not an error. A
// missing/expired credential yields ErrLoginRequired.
func (c *Client) PullSubstrate(ctx context.Context, slug string) ([]SubstrateFile, error) {
	resp, err := c.doAuthed(ctx, http.MethodGet, substratePath(slug)+"?full=1", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			Files []substrateFileWire `json:"files"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("hosted: decode substrate pull: %w", err)
		}
		files := make([]SubstrateFile, 0, len(out.Files))
		for _, w := range out.Files {
			content, err := base64.StdEncoding.DecodeString(w.Content)
			if err != nil {
				return nil, fmt.Errorf("hosted: substrate pull: bad base64 for %q: %w", w.Path, err)
			}
			files = append(files, SubstrateFile{Path: w.Path, Kind: w.Kind, Content: content})
		}
		return files, nil
	case http.StatusUnauthorized:
		return nil, ErrLoginRequired
	case http.StatusNotFound:
		return nil, fmt.Errorf("hosted: substrate pull: project %q not found (or you are not a member)", slug)
	default:
		return nil, fmt.Errorf("hosted: substrate pull: %s", serverError(resp.StatusCode, body))
	}
}
