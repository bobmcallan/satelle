package hosted

// Hosted REST hold client (sty_dec88606). Consumes satelle-server
// sty_18944955: POST checkout/release/takeover and GET checkout-log.
// JSON field names match the server (location_id, last_seen_at). Work-state
// ingest/list stay on gRPC Sync; this file does not revive them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrHeldElsewhere is the sentinel for HTTP 409 code=held.
var ErrHeldElsewhere = errors.New("hosted: story held by another location")

// HoldState is one checkout as the server returns it.
type HoldState struct {
	LocationID string `json:"location_id"`
	Label      string `json:"label"`
	UserID     string `json:"user_id"`
	LastSeenAt string `json:"last_seen_at"`
}

// HeldError names the holding location so the caller can take over.
type HeldError struct {
	ItemID string
	Hold   HoldState
}

func (e *HeldError) Error() string {
	seen := strings.TrimSpace(e.Hold.LastSeenAt)
	if seen != "" {
		seen = ", last seen " + seen
	}
	label := strings.TrimSpace(e.Hold.Label)
	if label != "" {
		label = " (" + label + ")"
	}
	id := e.ItemID
	if id == "" {
		id = "story"
	}
	return fmt.Sprintf("%s is held by location %s%s%s — satelle story hold takeover %s",
		id, e.Hold.LocationID, label, seen, id)
}

func (e *HeldError) Unwrap() error { return ErrHeldElsewhere }

func holdItemRoute(project, id, action string) string {
	p := "/api/v1/projects/" + url.PathEscape(project) + "/workstate/items/" + url.PathEscape(id)
	if action != "" {
		p += "/" + action
	}
	return p
}

func decodeHold(body []byte) HoldState {
	var h HoldState
	_ = json.Unmarshal(body, &h)
	return h
}

func (c *Client) postHold(ctx context.Context, project, id, action string, payload []byte) (*http.Response, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	return c.doAuthed(ctx, http.MethodPost, holdItemRoute(project, id, action), payload, contentJSON)
}

func (c *Client) mapHoldResponse(itemID string, resp *http.Response) (HoldState, error) {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return decodeHold(body), nil
	case http.StatusUnauthorized:
		return HoldState{}, ErrLoginRequired
	case http.StatusConflict:
		var env struct {
			Code string    `json:"code"`
			Hold HoldState `json:"hold"`
		}
		_ = json.Unmarshal(body, &env)
		held := env.Hold
		if held.LocationID == "" {
			held = decodeHold(body)
		}
		if c.location != "" && held.LocationID == c.location {
			return held, nil // same-location 409 is idempotent success
		}
		return HoldState{}, &HeldError{ItemID: itemID, Hold: held}
	case http.StatusForbidden:
		return HoldState{}, fmt.Errorf("hosted: not the holder: %s", serverError(resp.StatusCode, body))
	default:
		return HoldState{}, fmt.Errorf("hosted: hold %s: %s", itemID, serverError(resp.StatusCode, body))
	}
}

func (c *Client) locationBody() []byte {
	b, _ := json.Marshal(map[string]string{"location_id": c.location})
	return b
}

// Checkout POSTs …/checkout. Idempotent when already held here.
func (c *Client) Checkout(ctx context.Context, project, id string) (HoldState, error) {
	resp, err := c.postHold(ctx, project, id, "checkout", c.locationBody())
	if err != nil {
		return HoldState{}, err
	}
	return c.mapHoldResponse(id, resp)
}

// ReleaseHold POSTs …/release. Holder-owner only (server authority).
func (c *Client) ReleaseHold(ctx context.Context, project, id string) error {
	resp, err := c.postHold(ctx, project, id, "release", []byte("{}"))
	if err != nil {
		return err
	}
	_, err = c.mapHoldResponse(id, resp)
	return err
}

// TakeoverResult is the new hold plus the previous holder (empty if none).
type TakeoverResult struct {
	Hold     HoldState
	Previous HoldState
}

// TakeoverHold POSTs …/takeover. Previous holder is read from GET item
// (the takeover response is only the new hold — satelle-server holdJSON).
func (c *Client) TakeoverHold(ctx context.Context, project, id string) (TakeoverResult, error) {
	prev, _ := c.ItemHold(ctx, project, id)
	resp, err := c.postHold(ctx, project, id, "takeover", c.locationBody())
	if err != nil {
		return TakeoverResult{}, err
	}
	h, err := c.mapHoldResponse(id, resp)
	if err != nil {
		return TakeoverResult{}, err
	}
	return TakeoverResult{Hold: h, Previous: prev}, nil
}

// ItemHold GETs one workstate item and returns its hold (empty if unheld).
// Consumes the hosted GET item surface; does not republish REST ingest/list.
func (c *Client) ItemHold(ctx context.Context, project, id string) (HoldState, error) {
	resp, err := c.doAuthed(ctx, http.MethodGet, holdItemRoute(project, id, ""), nil, "")
	if err != nil {
		return HoldState{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return HoldState{}, ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return HoldState{}, fmt.Errorf("hosted: GET item %s: %s", id, serverError(resp.StatusCode, body))
	}
	var env struct {
		Hold HoldState `json:"hold"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Hold, nil
}

// HoldLog GETs …/checkout-log. Client-only this slice (ACs name the three
// verbs); kept so takeover/debug can cite the audit without a CLI subcommand.
func (c *Client) HoldLog(ctx context.Context, project, id string) ([]map[string]any, error) {
	resp, err := c.doAuthed(ctx, http.MethodGet, holdItemRoute(project, id, "checkout-log"), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hosted: checkout-log %s: %s", id, serverError(resp.StatusCode, body))
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("hosted: decode checkout-log: %w", err)
	}
	return out, nil
}

// FormatLastSeen renders an RFC3339 timestamp plus relative age.
func FormatLastSeen(rfc3339 string) string {
	s := strings.TrimSpace(rfc3339)
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return s
		}
	}
	age := time.Since(t.UTC()).Truncate(time.Second)
	if age < 0 {
		age = 0
	}
	return s + " (" + age.String() + " ago)"
}
