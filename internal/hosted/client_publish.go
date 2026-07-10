package hosted

// Publish catalog client (epic:sync-publish). Team workspace holds
// publisher-owned artifacts; not a sync destination.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrPublishNotFound is returned when a published path does not exist.
var ErrPublishNotFound = errors.New("published artifact not found")

// ErrNotPublisher is returned when the caller cannot append a version.
var ErrNotPublisher = errors.New("not the publisher of this artifact")

// PublishedItem is one catalog head (or history entry).
type PublishedItem struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Version     int    `json:"version"`
	BlobSHA256  string `json:"blob_sha256"`
	Size        int64  `json:"size"`
	PublisherID string `json:"publisher_id"`
	Title       string `json:"title"`
	CreatedAt   string `json:"created_at"`
	Created     bool   `json:"created,omitempty"`
}

func publishedRoute(wsID string) string {
	return "/api/v1/workspaces/" + url.PathEscape(wsID) + "/published"
}

func publishedPathRoute(wsID, path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return publishedRoute(wsID) + "/" + strings.Join(segs, "/")
}

// ListPublished returns latest heads in a workspace catalog.
func (c *Client) ListPublished(ctx context.Context, wsID string) ([]PublishedItem, error) {
	resp, err := c.doAuthed(ctx, http.MethodGet, publishedRoute(wsID), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hosted: GET published: %s", serverError(resp.StatusCode, body))
	}
	var out struct {
		Items []PublishedItem `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("hosted: decode published: %w", err)
	}
	if out.Items == nil {
		out.Items = []PublishedItem{}
	}
	return out.Items, nil
}

// PublishFile PUTs raw bytes as a new published version. kind is required.
func (c *Client) PublishFile(ctx context.Context, wsID, path, kind, title string, content []byte) (PublishedItem, error) {
	if content == nil {
		content = []byte{}
	}
	q := url.Values{"kind": {kind}}
	if title != "" {
		q.Set("title", title)
	}
	route := publishedPathRoute(wsID, path) + "?" + q.Encode()
	resp, err := c.doAuthed(ctx, http.MethodPut, route, content, "text/plain; charset=utf-8")
	if err != nil {
		return PublishedItem{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var item PublishedItem
		if len(body) > 0 {
			if err := json.Unmarshal(body, &item); err != nil {
				return PublishedItem{}, fmt.Errorf("hosted: decode publish result: %w", err)
			}
		}
		item.Created = resp.StatusCode == http.StatusCreated || item.Created
		return item, nil
	case http.StatusForbidden:
		return PublishedItem{}, ErrNotPublisher
	case http.StatusUnauthorized:
		return PublishedItem{}, ErrLoginRequired
	default:
		return PublishedItem{}, fmt.Errorf("hosted: PUT published: %s", serverError(resp.StatusCode, body))
	}
}

// PublishedContent GETs one path's content. version 0 = latest.
func (c *Client) PublishedContent(ctx context.Context, wsID, path string, version int) ([]byte, PublishedItem, error) {
	route := publishedPathRoute(wsID, path)
	if version > 0 {
		route += "?version=" + strconv.Itoa(version)
	}
	resp, err := c.doAuthed(ctx, http.MethodGet, route, nil, "")
	if err != nil {
		return nil, PublishedItem{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		meta := PublishedItem{Path: path}
		if v := resp.Header.Get("X-Satelle-Publish-Version"); v != "" {
			meta.Version, _ = strconv.Atoi(v)
		}
		meta.Kind = resp.Header.Get("X-Satelle-Publish-Kind")
		meta.PublisherID = resp.Header.Get("X-Satelle-Publisher-Id")
		meta.BlobSHA256 = strings.Trim(resp.Header.Get("ETag"), `"`)
		return body, meta, nil
	case http.StatusNotFound:
		return nil, PublishedItem{}, ErrPublishNotFound
	case http.StatusUnauthorized:
		return nil, PublishedItem{}, ErrLoginRequired
	default:
		return nil, PublishedItem{}, fmt.Errorf("hosted: GET published content: %s", serverError(resp.StatusCode, body))
	}
}
