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

// DeclaredTrigger is one trigger a remote declares to the server. Params are the
// definition's trigger params, forwarded untouched (the server validates the
// kinds it knows and stores the rest opaquely); Paused, UpdatedAt and NextFireAt
// are filled by the server on a list.
type DeclaredTrigger struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Params     map[string]string `json:"params,omitempty"`
	Paused     bool              `json:"paused,omitempty"`
	UpdatedAt  string            `json:"updated_at,omitempty"`
	NextFireAt string            `json:"next_fire_at,omitempty"`
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

// WakeFailedError is the server's 502 on a fire whose wake it could not deliver:
// the server still recorded the fire (under TriggerID, when it names it) with
// the outcome wake_failed, so the fire happened and only the delivery did not.
type WakeFailedError struct {
	Trigger   string
	Reason    string
	TriggerID string
}

func (e *WakeFailedError) Error() string {
	name := e.Trigger
	if name == "" {
		name = "(unnamed)"
	}
	msg := fmt.Sprintf("trigger %s fired, wake failed: %s; fire recorded", name, e.Reason)
	if e.TriggerID != "" {
		msg += " as " + e.TriggerID
	}
	return msg
}

// TriggerInvalidError is the server's 400 on a registration: its reason and, when
// it names one, the trigger it rejected. The client never judges params itself.
type TriggerInvalidError struct {
	Trigger string
	Reason  string
}

func (e *TriggerInvalidError) Error() string {
	if e.Trigger == "" {
		return "trigger registration rejected: " + e.Reason
	}
	return fmt.Sprintf("trigger %s rejected: %s", e.Trigger, e.Reason)
}

// RemoteNotFoundError is the server's 404 on a retire: the remote does not exist
// for this caller, or it was already retired.
type RemoteNotFoundError struct{ RemoteID string }

func (e *RemoteNotFoundError) Error() string {
	return fmt.Sprintf("remote %s not found or already retired", e.RemoteID)
}

// RemoteForbiddenError is the server's 403 on a retire: the caller is a member of
// the remote's project but neither its creator nor an admin.
type RemoteForbiddenError struct{ RemoteID string }

func (e *RemoteForbiddenError) Error() string {
	return fmt.Sprintf("only the remote's creator or an admin of its project may retire remote %s", e.RemoteID)
}

// wakeFailedCode is the outcome the server records for such a fire.
const wakeFailedCode = "wake_failed"

func remoteRoute(remoteID, tail string) string {
	return "/api/v1/remotes/" + url.PathEscape(remoteID) + tail
}

// remoteCall sends one authenticated request and returns the body of a 2xx
// answer. A 409 is returned as the refusal's reason and recorded id, a 400 on a
// PUT (a registration) as a *TriggerInvalidError, a 502 that says the wake failed as a *WakeFailedError,
// a 403 or 404 on a DELETE (a retire) as a *RemoteForbiddenError or
// *RemoteNotFoundError; any other non-2xx is a concise server error.
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
	case resp.StatusCode == http.StatusBadRequest && method == http.MethodPut:
		return nil, &TriggerInvalidError{Reason: apiReason(body, "HTTP 400"), Trigger: bodyTrigger(body)}
	case resp.StatusCode == http.StatusForbidden && method == http.MethodDelete:
		return nil, &RemoteForbiddenError{}
	case resp.StatusCode == http.StatusNotFound && method == http.MethodDelete:
		return nil, &RemoteNotFoundError{}
	case resp.StatusCode == http.StatusConflict:
		return nil, &TriggerRefusedError{Reason: apiReason(body, "HTTP 409"), TriggerID: bodyTriggerID(body)}
	case resp.StatusCode == http.StatusBadGateway && isWakeFailure(body):
		return nil, &WakeFailedError{Reason: apiReason(body, "HTTP 502"), TriggerID: bodyTriggerID(body)}
	default:
		return nil, fmt.Errorf("hosted: %s %s: %s", method, path, serverError(resp.StatusCode, body))
	}
}

// apiReason is the server's own sentence for an error answer, else its code,
// else fallback.
func apiReason(body []byte, fallback string) string {
	if e, ok := decodeAPIError(body); ok {
		if m := strings.TrimSpace(e.Message); m != "" {
			return m
		}
		if m := strings.TrimSpace(e.Error); m != "" {
			return m
		}
	}
	return fallback
}

// isWakeFailure reports whether a 502 body says the wake could not be
// delivered, by its code or its sentence; any other 502 (a proxy's, say) is not
// a recorded fire.
func isWakeFailure(body []byte) bool {
	e, ok := decodeAPIError(body)
	if !ok {
		return false
	}
	if strings.TrimSpace(e.Error) == wakeFailedCode {
		return true
	}
	return strings.Contains(strings.ToLower(e.Message+" "+e.Error), "wake failed")
}

// bodyTrigger is the trigger name an error body carries, when the server names one.
func bodyTrigger(body []byte) string {
	var r struct {
		Trigger string `json:"trigger"`
		Name    string `json:"name"`
	}
	_ = json.Unmarshal(body, &r)
	if r.Trigger != "" {
		return r.Trigger
	}
	return r.Name
}

func bodyTriggerID(body []byte) string {
	var r FireResult
	_ = json.Unmarshal(body, &r)
	return r.TriggerID
}

// PutDeclaredTriggers replaces the remote's declared set with triggers
// (PUT …/declared-triggers), each with its params as given (the field is left out
// when they are empty). The server keeps the paused state of kept names; its 400
// is a *TriggerInvalidError.
func (c *Client) PutDeclaredTriggers(ctx context.Context, remoteID string, triggers []DeclaredTrigger) error {
	out := make([]DeclaredTrigger, len(triggers))
	for i, t := range triggers {
		out[i] = DeclaredTrigger{Name: t.Name, Kind: t.Kind, Params: t.Params}
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
// story; either may be empty. A 409 returns a *TriggerRefusedError and a 502
// wake failure a *WakeFailedError.
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
// an unnamed fire. Source is the surface the fire came through, as the server
// states it; Outcome is what the server recorded, such as wake_failed.
type FireRecord struct {
	ID        string `json:"id"`
	TriggerID string `json:"trigger_id"`
	Trigger   string `json:"trigger"`
	Source    string `json:"source,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
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

// RetireRemote retires the remote on the server (DELETE /api/v1/remotes/{id}): it
// drops out of every list, its declared triggers are deleted and its name is
// freed, while its fire history stays readable and the user's wake secret is
// untouched. A 404 is a *RemoteNotFoundError (unknown, not a member, or already
// retired) and a 403 a *RemoteForbiddenError. Only the explicit `satelle remote
// retire` command may call it: stopping or tearing down a node never retires its
// remote.
func (c *Client) RetireRemote(ctx context.Context, remoteID string) error {
	_, err := c.remoteCall(ctx, http.MethodDelete, remoteRoute(remoteID, ""), nil)
	var nf *RemoteNotFoundError
	if errors.As(err, &nf) {
		nf.RemoteID = remoteID
	}
	var fb *RemoteForbiddenError
	if errors.As(err, &fb) {
		fb.RemoteID = remoteID
	}
	return err
}

// nameRefusal fills the trigger name into a refusal or wake failure the server
// answered without one.
func nameRefusal(err error, trigger string) {
	var refused *TriggerRefusedError
	if errors.As(err, &refused) {
		refused.Trigger = trigger
	}
	var failed *WakeFailedError
	if errors.As(err, &failed) {
		failed.Trigger = trigger
	}
}
