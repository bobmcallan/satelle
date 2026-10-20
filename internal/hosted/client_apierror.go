package hosted

// The typed answer to a request the server refused, and the two calls that
// return it: authedCall (the signed-in user's bearer) and SignedCall (a remote
// node's own signature). Both build the error from decodeAPIError, the one
// parser of the server's error body, so a refusal reads the same on either path.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIError is the server's refusal of a request: its status, the machine code
// it gave (its "error") and the reason (its "reason", else its "message"). The
// code and reason are the server's words, passed on as they came.
type APIError struct {
	Status int
	Code   string
	Reason string
	// body is the answer as received, for the callers that layer their own
	// mapping on it (remoteCall).
	body []byte
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Reason != "":
		return e.Code + ": " + e.Reason
	case e.Code != "":
		return e.Code
	case e.Reason != "":
		return e.Reason
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// newAPIError is the error for a non-2xx answer with body. Its code and reason
// come from decodeAPIError; a body that is not the server's JSON leaves the
// status text as the code.
func newAPIError(status int, body []byte) *APIError {
	e := &APIError{Status: status, body: body}
	if parsed, ok := decodeAPIError(body); ok {
		e.Code = strings.TrimSpace(parsed.Error)
		e.Reason = strings.TrimSpace(parsed.Reason)
		if e.Reason == "" {
			e.Reason = strings.TrimSpace(parsed.Message)
		}
	}
	if e.Code == "" && e.Reason == "" {
		e.Code = http.StatusText(status)
	}
	return e
}

// maxCallBody bounds what authedCall and SignedCall read of an answer.
const maxCallBody = 1 << 20

// authedCall sends one request with the signed-in user's bearer and returns the
// body of a 2xx answer. payload, when not nil, is sent as JSON. A 401 is
// ErrLoginRequired and any other non-2xx an *APIError; a request body is never
// put in an error.
func (c *Client) authedCall(ctx context.Context, method, path string, payload any) ([]byte, error) {
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCallBody))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrLoginRequired
	}
	return nil, newAPIError(resp.StatusCode, body)
}

// SignedRequest is one request a remote node makes on its own authority: the
// signature headers (or a workload identity token as Bearer) authenticate it, and
// no satelle sign-in is involved.
type SignedRequest struct {
	Method, Path string
	// Header carries the signature headers.
	Header map[string]string
	// Bearer, when set, is a workload identity token sent as Authorization.
	Bearer string
	// Body, when not nil, is sent as JSON.
	Body any
}

// SignedCall sends req with no satelle bearer and returns the body of a 2xx
// answer, else an *APIError carrying the server's code and reason. It does not
// retry: a signature carries a nonce the server has then seen.
func (c *Client) SignedCall(ctx context.Context, req SignedRequest) ([]byte, error) {
	var rd io.Reader
	if req.Body != nil {
		raw, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("hosted: encode %s: %w", req.Path, err)
		}
		rd = bytes.NewReader(raw)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, c.server+req.Path, rd)
	if err != nil {
		return nil, err
	}
	if req.Body != nil {
		hr.Header.Set("Content-Type", contentJSON)
	}
	for k, v := range req.Header {
		hr.Header.Set(k, v)
	}
	if req.Bearer != "" {
		hr.Header.Set("Authorization", "Bearer "+req.Bearer)
	}
	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("hosted: %s %s: %w", req.Method, req.Path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCallBody))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}
	return nil, newAPIError(resp.StatusCode, body)
}
