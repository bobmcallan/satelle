package remotenode

// A node reads the secrets of its run from satelle.dev, one signed request per
// secret (sty_0b1c72a9). Every request names the run it is bound to and is
// authenticated by the remote's own key (remotesign) or its platform's workload
// identity token, never by a satelle login. A value is returned to the caller in
// memory and goes nowhere else: not to disk, the ledger, a log or an error.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotesign"
)

// RefreshEvery is how often a long run asks satelle.dev to keep its run alive; a
// run lives 30 minutes from its last refresh.
const RefreshEvery = 10 * time.Minute

// SecretRefusal is satelle.dev's refusal of one secret's read, with the server's
// own code and reason. The agent is not started on one.
type SecretRefusal struct {
	Key string
	Err *hosted.APIError
}

func (e *SecretRefusal) Error() string { return fmt.Sprintf("secret %s refused: %s", e.Key, e.Err) }
func (e *SecretRefusal) Unwrap() error { return e.Err }

// Auth proves one request to satelle.dev: it sets the signature headers, or the
// workload identity token, on sr for the signed message req.
type Auth interface {
	Apply(ctx context.Context, sr *hosted.SignedRequest, req remotesign.Request) error
}

type keyAuth struct{ signer *remotesign.Signer }

func (a keyAuth) Apply(_ context.Context, sr *hosted.SignedRequest, req remotesign.Request) error {
	h, err := a.signer.Sign(req)
	if err != nil {
		return err
	}
	sr.Header = h.Map()
	return nil
}

// oidcAuth mints a fresh token for the run's audience on every request: the server
// takes a token's jti once.
type oidcAuth struct {
	cmds     *Commands
	audience string
}

func (a oidcAuth) Apply(ctx context.Context, sr *hosted.SignedRequest, _ remotesign.Request) error {
	tok, err := a.cmds.Token(ctx, a.audience)
	if err != nil {
		return err
	}
	sr.Bearer = tok
	return nil
}

// NewAuth is how rs's remote proves itself on this machine: with the private key
// getenv returns for the variable the identity names, or with a token its
// platform issues. The key is only ever read from the environment here.
func NewAuth(rs RunSecrets, getenv func(string) string) (Auth, error) {
	switch {
	case rs.IsOIDC():
		argv, err := rs.OIDCTokenArgv()
		if err != nil {
			return nil, err
		}
		def := remotedef.Definition{Identity: remotedef.Identity{OIDC: &remotedef.OIDCIdentity{Token: argv}}}
		return oidcAuth{cmds: &Commands{Def: def}, audience: strings.TrimRight(rs.Server, "/") + "/runs/" + rs.ServerRunID}, nil
	case rs.IdentityEnv() != "":
		seed := getenv(rs.IdentityEnv())
		if seed == "" {
			return nil, fmt.Errorf("the remote's private key is not in the environment variable %s", rs.IdentityEnv())
		}
		signer, err := remotesign.NewSigner(seed)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rs.IdentityEnv(), err)
		}
		return keyAuth{signer}, nil
	}
	return nil, errors.New("the remote declares no identity to prove itself with")
}

// Reader reads one run's secrets from satelle.dev.
type Reader struct {
	rs     RunSecrets
	client *hosted.Client
	auth   Auth
}

// ReaderOption adjusts a Reader; tests inject their HTTP client here.
type ReaderOption func(*Reader, *http.Client)

// WithHTTPClient replaces the HTTP client calls go through.
func WithHTTPClient(h *http.Client) ReaderOption {
	return func(_ *Reader, dst *http.Client) { *dst = *h }
}

// NewReader returns the Reader for rs, which must name a run and a remote.
func NewReader(rs RunSecrets, auth Auth, opts ...ReaderOption) (*Reader, error) {
	if err := rs.Validate(); err != nil {
		return nil, fmt.Errorf("remote secrets cannot be read: %w", err)
	}
	r := &Reader{rs: rs, auth: auth}
	var hc http.Client
	hc.Timeout = 30 * time.Second
	for _, o := range opts {
		o(r, &hc)
	}
	r.client = hosted.NewClient(rs.Server, nil, &hc)
	return r, nil
}

func (r *Reader) runPath(tail string) string {
	return "/api/v1/remotes/" + r.rs.RemoteID + "/runs/" + r.rs.ServerRunID + tail
}

// call sends one signed request for the run and returns the body of a 2xx answer.
func (r *Reader) call(ctx context.Context, method, tail string, ref SecretRef, body any) ([]byte, error) {
	path := r.runPath(tail)
	sr := hosted.SignedRequest{Method: method, Path: path, Body: body}
	if err := r.auth.Apply(ctx, &sr, remotesign.Request{
		Method: method, Path: path, TriggerID: r.rs.ServerRunID, Key: ref.Key, Scope: ref.Scope, Workspace: ref.Workspace,
	}); err != nil {
		return nil, err
	}
	return r.client.SignedCall(ctx, sr)
}

type readBody struct {
	Scope     string `json:"scope"`
	Workspace string `json:"workspace,omitempty"`
}

// Read reads every declared secret once, for the run, and returns key → value. The
// first secret the server refuses is a *SecretRefusal and nothing is returned.
func (r *Reader) Read(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.rs.Secrets))
	for _, ref := range r.rs.Secrets {
		raw, err := r.call(ctx, http.MethodPost, "/secrets/"+url.PathEscape(ref.Key), ref, readBody{Scope: ref.Scope, Workspace: ref.Workspace})
		if err != nil {
			var ae *hosted.APIError
			if errors.As(err, &ae) {
				return nil, &SecretRefusal{Key: ref.Key, Err: ae}
			}
			return nil, fmt.Errorf("secret %s: %w", ref.Key, err)
		}
		var v struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return nil, fmt.Errorf("secret %s: the answer is malformed", ref.Key)
		}
		out[ref.Key] = v.Value
	}
	return out, nil
}

// Refresh asks satelle.dev to keep the run alive for another 30 minutes.
func (r *Reader) Refresh(ctx context.Context) error {
	_, err := r.call(ctx, http.MethodPost, "/refresh", SecretRef{}, nil)
	return err
}

// End ends the run: no read is answered for it afterwards.
func (r *Reader) End(ctx context.Context) error {
	_, err := r.call(ctx, http.MethodDelete, "", SecretRef{}, nil)
	return err
}

// KeepAlive refreshes the run every interval until the returned stop is called or
// ctx ends. A refresh that fails is not fatal: the run simply lapses on its own
// clock.
func (r *Reader) KeepAlive(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = r.Refresh(ctx)
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

// SecretsForRun is the one call that obtains a run's secret values: it proves the
// remote with the key or identity rs names (read from getenv), reads each declared
// secret once, and returns them. Nothing is stored.
func SecretsForRun(ctx context.Context, rs RunSecrets, getenv func(string) string, opts ...ReaderOption) (map[string]string, *Reader, error) {
	auth, err := NewAuth(rs, getenv)
	if err != nil {
		return nil, nil, err
	}
	r, err := NewReader(rs, auth, opts...)
	if err != nil {
		return nil, nil, err
	}
	values, err := r.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	return values, r, nil
}
