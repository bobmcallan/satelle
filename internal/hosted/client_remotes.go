package hosted

// Remote declared-triggers client (sty_a9768210). Consumes satelle-server
// sty_e8ca6ba4: the routes are keyed by the remote's server id and the server
// enforces project membership on that id, so no project is sent — not in the
// path, a query, a header or a body.

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

// DeclaredTrigger is one trigger a remote declares to the server. Paused and
// UpdatedAt are filled by the server on a list.
type DeclaredTrigger struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Paused    bool   `json:"paused,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// FireResult is what the server answers a fire with: the fire-record id.
type FireResult struct {
	TriggerID string `json:"trigger_id"`
}

// TriggerRefusedError is the server's 409 on a fire (an undeclared or paused
// trigger): the reason it gave and the fire-record id it recorded the refusal
// under.
type TriggerRefusedError struct {
	Trigger   string
	Reason    string
	TriggerID string
}

func (e *TriggerRefusedError) Error() string {
	name := e.Trigger
	if name == "" {
		name = "(unnamed)"
	}
	msg := fmt.Sprintf("trigger %s refused: %s", name, e.Reason)
	if e.TriggerID != "" {
		msg += " (recorded as " + e.TriggerID + ")"
	}
	return msg
}

func remoteRoute(remoteID, tail string) string {
	return "/api/v1/remotes/" + url.PathEscape(remoteID) + tail
}

// remoteCall sends one authenticated request and returns the body of a 2xx
// answer. A 409 is returned as the refusal's reason and recorded id; any other
// non-2xx is a concise server error.
func (c *Client) remoteCall(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var raw []byte
	ctype := ""
	if payload != nil {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return nil, fmt.Errorf("hosted: encode %s: %w", path, err)
		}
		ctype = contentJSON
	}
	resp, err := c.doAuthed(ctx, method, path, raw, ctype)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrLoginRequired
	case resp.StatusCode == http.StatusConflict:
		return nil, &TriggerRefusedError{Reason: conflictReason(body), TriggerID: conflictTriggerID(body)}
	default:
		return nil, fmt.Errorf("hosted: %s %s: %s", method, path, serverError(resp.StatusCode, body))
	}
}

// conflictReason is the server's own sentence for a 409, else its code.
func conflictReason(body []byte) string {
	if e, ok := decodeAPIError(body); ok {
		if m := strings.TrimSpace(e.Message); m != "" {
			return m
		}
		if m := strings.TrimSpace(e.Error); m != "" {
			return m
		}
	}
	return "HTTP 409"
}

func conflictTriggerID(body []byte) string {
	var r FireResult
	_ = json.Unmarshal(body, &r)
	return r.TriggerID
}

// PutDeclaredTriggers replaces the remote's declared set with triggers
// (PUT …/declared-triggers). The server keeps the paused state of kept names.
func (c *Client) PutDeclaredTriggers(ctx context.Context, remoteID string, triggers []DeclaredTrigger) error {
	out := make([]DeclaredTrigger, len(triggers))
	for i, t := range triggers {
		out[i] = DeclaredTrigger{Name: t.Name, Kind: t.Kind}
	}
	_, err := c.remoteCall(ctx, http.MethodPut, remoteRoute(remoteID, "/declared-triggers"), map[string]any{"triggers": out})
	return err
}

// ListDeclaredTriggers returns the remote's declared triggers with their paused
// state (GET …/declared-triggers).
func (c *Client) ListDeclaredTriggers(ctx context.Context, remoteID string) ([]DeclaredTrigger, error) {
	var out []DeclaredTrigger
	if err := c.getJSON(ctx, remoteRoute(remoteID, "/declared-triggers"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FireRemote fires the remote (POST …/fire), naming a declared trigger and/or a
// story; either may be empty. A 409 returns a *TriggerRefusedError.
func (c *Client) FireRemote(ctx context.Context, remoteID, storyID, trigger string) (FireResult, error) {
	body := map[string]string{}
	if storyID != "" {
		body["story_id"] = storyID
	}
	if trigger != "" {
		body["trigger"] = trigger
	}
	raw, err := c.remoteCall(ctx, http.MethodPost, remoteRoute(remoteID, "/fire"), body)
	if err != nil {
		nameRefusal(err, trigger)
		return FireResult{}, err
	}
	var res FireResult
	_ = json.Unmarshal(raw, &res)
	return res, nil
}

// FireRecord is one fire the server recorded (GET …/triggers). The record's id
// is what a wake carries as trigger_id; Trigger is the declared name, empty for
// an unnamed fire.
type FireRecord struct {
	ID        string `json:"id"`
	TriggerID string `json:"trigger_id"`
	Trigger   string `json:"trigger"`
}

// FireID is the record's id, whichever field the server carried it in.
func (r FireRecord) FireID() string {
	if r.ID != "" {
		return r.ID
	}
	return r.TriggerID
}

// ListFireRecords returns the fires the server recorded for the remote
// (GET …/triggers), refused ones included.
func (c *Client) ListFireRecords(ctx context.Context, remoteID string) ([]FireRecord, error) {
	var out []FireRecord
	if err := c.getJSON(ctx, remoteRoute(remoteID, "/triggers"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetTriggerPaused pauses or resumes one declared trigger
// (POST …/declared-triggers/{name}/pause|resume).
func (c *Client) SetTriggerPaused(ctx context.Context, remoteID, name string, paused bool) error {
	action := "resume"
	if paused {
		action = "pause"
	}
	_, err := c.remoteCall(ctx, http.MethodPost, remoteRoute(remoteID, "/declared-triggers/"+url.PathEscape(name)+"/"+action), struct{}{})
	nameRefusal(err, name)
	return err
}

// nameRefusal fills the trigger name into a refusal the server answered without
// one.
func nameRefusal(err error, trigger string) {
	var refused *TriggerRefusedError
	if errors.As(err, &refused) {
		refused.Trigger = trigger
	}
}
