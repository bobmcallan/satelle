package hosted

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SignedCall builds its error from the one parser of the server's error body.
func TestSignedCallBuildsItsErrorFromTheOneParser(t *testing.T) {
	const body = `{"error":"refused","reason":"no_grant","message":"ignored when reason is set"}`
	want, ok := decodeAPIError([]byte(body))
	if !ok {
		t.Fatal("decodeAPIError rejected the body")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	c := NewClient(ts.URL, &memStore{}, ts.Client())

	_, err := c.SignedCall(context.Background(), SignedRequest{Method: http.MethodPost, Path: "/x", Header: map[string]string{"X-Satelle-Signature": "s"}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden || ae.Code != want.Error || ae.Reason != want.Reason {
		t.Errorf("SignedCall error %+v vs parser %+v", ae, want)
	}
	if got := ae.Error(); got != "refused: no_grant" {
		t.Errorf("Error() = %q", got)
	}
	if e := newAPIError(502, []byte("<html>")); e.Code != "Bad Gateway" || e.Status != 502 {
		t.Errorf("a non-JSON body falls back to the status text: %+v", e)
	}
}

func TestSignedCallSendsNoBearerAndTheGivenHeaders(t *testing.T) {
	var auth, sig, ctype string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, sig, ctype = r.Header.Get("Authorization"), r.Header.Get("X-Satelle-Signature"), r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"value":"v"}`))
	}))
	defer ts.Close()
	// A client with NO stored credential still makes a signed call.
	c := NewClient(ts.URL, &memStore{}, ts.Client())
	out, err := c.SignedCall(context.Background(), SignedRequest{Method: "POST", Path: "/p", Header: map[string]string{"X-Satelle-Signature": "sigval"}, Body: map[string]string{"scope": "personal"}})
	if err != nil || !strings.Contains(string(out), `"value":"v"`) {
		t.Fatalf("out = %s, %v", out, err)
	}
	if auth != "" || sig != "sigval" || ctype != "application/json" {
		t.Errorf("auth=%q sig=%q ctype=%q", auth, sig, ctype)
	}
	if _, err := c.SignedCall(context.Background(), SignedRequest{Method: "POST", Path: "/p", Bearer: "oidc-token"}); err != nil || auth != "Bearer oidc-token" {
		t.Errorf("a workload token is the bearer: %q, %v", auth, err)
	}
}
