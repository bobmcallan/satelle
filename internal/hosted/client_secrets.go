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

// The satelle-server secrets contract this file mirrors (sty_d9acc2b5). A value
// goes up and comes back down exactly once each way: PutSecret sends it, and
// ReadSecret returns it to a live run of a remote that holds a grant. No other
// route here returns a value, and SecretMeta has no field that could hold one.
//
//	PUT    /api/v1/secrets/{key}                          {value, personal}
//	GET    /api/v1/secrets                                [{key, has_value, personal, readers}]
//	DELETE /api/v1/secrets/{key}
//	PUT    /api/v1/secrets/{key}/grants/{remote}          {personal}
//	DELETE /api/v1/secrets/{key}/grants/{remote}
//	POST   /api/v1/remotes/{remote}/runs                  {wake_key} → {run}
//	POST   /api/v1/remotes/{remote}/runs/{run}:refresh
//	DELETE /api/v1/remotes/{remote}/runs/{run}
//	POST   /api/v1/remotes/{remote}/runs/{run}/secrets/{key}:read   {nonce} → {value}
//
// The read answers 403 with a reason when the remote holds no grant on the key,
// the run is not live, the key is the run's waker-only key, a personal key is
// read by a remote others can fire, or the (run, key, nonce) was already used.

// SecretMeta is what the server tells any reader about a secret: never its value.
type SecretMeta struct {
	Key      string   `json:"key"`
	HasValue bool     `json:"has_value"`
	Personal bool     `json:"personal"`
	Readers  []string `json:"readers"`
}

// SecretRefusedError is a refusal of a secret read or grant: the server's 403 (or
// 404) with the reason it gave.
type SecretRefusedError struct {
	Key    string
	Reason string
}

func (e *SecretRefusedError) Error() string {
	return fmt.Sprintf("secret %s refused: %s", e.Key, e.Reason)
}

// secretsRoute is the secrets collection root.
const secretsRoute = "/api/v1/secrets"

func secretRoute(key string) string { return secretsRoute + "/" + url.PathEscape(key) }

func grantRoute(key, remote string) string {
	return secretRoute(key) + "/grants/" + url.PathEscape(remote)
}

func runsRoute(remote string) string { return "/api/v1/remotes/" + url.PathEscape(remote) + "/runs" }

func runRoute(remote, run string) string { return runsRoute(remote) + "/" + url.PathEscape(run) }

// secretCall sends one request and returns the response body of a 2xx. A 403 or
// 404 becomes a *SecretRefusedError for key; a 401 is ErrLoginRequired; anything
// else is a clean server error. payload is never put in an error.
func (c *Client) secretCall(ctx context.Context, method, path, key string, payload any) ([]byte, error) {
	var raw []byte
	var ct string
	if payload != nil {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			return nil, err
		}
		ct = contentJSON
	}
	resp, err := c.doAuthed(ctx, method, path, raw, ct)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrLoginRequired
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		return nil, &SecretRefusedError{Key: key, Reason: refusalReason(resp.StatusCode, body)}
	default:
		return nil, fmt.Errorf("hosted: %s %s: %s", method, path, serverError(resp.StatusCode, body))
	}
}

// refusalReason is the reason a refusal body gives: a "reason" field, else the
// server's message or error code, else the status.
func refusalReason(code int, body []byte) string {
	var r struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &r) == nil {
		for _, s := range []string{r.Reason, r.Message, r.Error} {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return fmt.Sprintf("HTTP %d", code)
}

// PutSecret uploads one secret. personal marks it readable only by the
// creator's own remotes that no one else can fire; the server enforces that.
func (c *Client) PutSecret(ctx context.Context, key, value string, personal bool) error {
	_, err := c.secretCall(ctx, http.MethodPut, secretRoute(key), key, map[string]any{"value": value, "personal": personal})
	return err
}

// ListSecrets returns every secret's metadata. The server returns no value.
func (c *Client) ListSecrets(ctx context.Context) ([]SecretMeta, error) {
	body, err := c.secretCall(ctx, http.MethodGet, secretsRoute, "", nil)
	if err != nil {
		return nil, err
	}
	var out []SecretMeta
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("hosted: decode secrets: %w", err)
	}
	return out, nil
}

// GrantSecret lets remote read key. personal also marks the key personal.
func (c *Client) GrantSecret(ctx context.Context, key, remote string, personal bool) error {
	_, err := c.secretCall(ctx, http.MethodPut, grantRoute(key, remote), key, map[string]any{"personal": personal})
	return err
}

// RevokeSecret removes remote's read of key.
func (c *Client) RevokeSecret(ctx context.Context, key, remote string) error {
	_, err := c.secretCall(ctx, http.MethodDelete, grantRoute(key, remote), key, nil)
	return err
}

// RegisterRun tells the server a run of remote is live and which key is the
// waker-only one (the definition's wake_key), and returns the run id.
func (c *Client) RegisterRun(ctx context.Context, remote, wakeKey string) (string, error) {
	body, err := c.secretCall(ctx, http.MethodPost, runsRoute(remote), "", map[string]any{"wake_key": wakeKey})
	if err != nil {
		return "", err
	}
	var out struct {
		Run string `json:"run"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Run == "" {
		return "", errors.New("hosted: the server returned no run id")
	}
	return out.Run, nil
}

// RefreshRun keeps a registered run live.
func (c *Client) RefreshRun(ctx context.Context, remote, run string) error {
	_, err := c.secretCall(ctx, http.MethodPost, runRoute(remote, run)+":refresh", "", nil)
	return err
}

// EndRun ends a registered run: no further read is answered for it.
func (c *Client) EndRun(ctx context.Context, remote, run string) error {
	_, err := c.secretCall(ctx, http.MethodDelete, runRoute(remote, run), "", nil)
	return err
}

// ReadSecret asks the server for key's value for one run of remote. nonce is
// single use per (run, key). A refusal is a *SecretRefusedError carrying the
// server's reason.
func (c *Client) ReadSecret(ctx context.Context, remote, run, key, nonce string) (string, error) {
	path := runRoute(remote, run) + "/secrets/" + url.PathEscape(key) + ":read"
	body, err := c.secretCall(ctx, http.MethodPost, path, key, map[string]any{"nonce": nonce})
	if err != nil {
		return "", err
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", errors.New("hosted: the secret answer is malformed")
	}
	return out.Value, nil
}
