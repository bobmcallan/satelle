package hosted

// The typed answer to a request the server refused, and SignedCall, the call a
// remote node makes on its own signature. The error is built from decodeAPIError,
// the one parser of the server's error body.

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
	e := &APIError{Status: status}
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

// maxCallBody bounds what SignedCall reads of an answer: room for the definition
// bundle a node fetches, which is the largest answer.
const maxCallBody = 16 << 20

// SignedRequest is one request a remote node makes on its own authority: its
// node token as Bearer (or request headers) authenticates it, and no satelle
// sign-in is involved.
type SignedRequest struct {
	Method, Path string
	// Header carries extra request headers.
	Header map[string]string
	// Bearer, when set, is sent as Authorization: a node's own token.
	Bearer string
	// Body, when not nil, is sent as JSON.
	Body any
}

// SignedCall sends req with no satelle bearer and returns the body of a 2xx
// answer, else an *APIError carrying the server's code and reason. It does not
// retry: a signature carries a nonce the server has then seen.
func (c *Client) SignedCall(ctx context.Context, req SignedRequest) ([]byte, error) {
	body, _, err := c.SignedDo(ctx, req)
	return body, err
}

// SignedDo is SignedCall that also returns the headers of a 2xx answer, for a
// caller that checks them (a node verifies the ETag of the definition it fetches).
func (c *Client) SignedDo(ctx context.Context, req SignedRequest) ([]byte, http.Header, error) {
	var rd io.Reader
	if req.Body != nil {
		raw, err := json.Marshal(req.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("hosted: encode %s: %w", req.Path, err)
		}
		rd = bytes.NewReader(raw)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, c.server+req.Path, rd)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("hosted: %s %s: %w", req.Method, req.Path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCallBody+1))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if len(body) > maxCallBody {
			return nil, nil, fmt.Errorf("hosted: %s %s: the answer is larger than %d bytes", req.Method, req.Path, maxCallBody)
		}
		return body, resp.Header, nil
	}
	return nil, nil, newAPIError(resp.StatusCode, body[:min(len(body), maxCallBody)])
}
