package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type seenCall struct {
	Method, Path, Auth string
	Body               string
}

// secretsServer records every request and answers each with the next canned
// (status, body), then the last one again.
func secretsServer(t *testing.T, answers ...[2]string) (*Client, *[]seenCall) {
	t.Helper()
	var seen []seenCall
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, seenCall{r.Method, r.URL.Path, r.Header.Get("Authorization"), string(b)})
		ans := answers[len(answers)-1]
		if len(seen) <= len(answers) {
			ans = answers[len(seen)-1]
		}
		code := 200
		if ans[0] != "" {
			code = map[string]int{"204": 204, "201": 201, "403": 403, "404": 404, "400": 400, "502": 502}[ans[0]]
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(ans[1]))
	}))
	t.Cleanup(ts.Close)
	store := &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "cli-token", RefreshToken: "r"}, set: true}
	return NewClient(ts.URL, store, ts.Client()), &seen
}

func TestPutSecretRoutesByScopeAndSendsExpiry(t *testing.T) {
	c, seen := secretsServer(t, [2]string{"204", ""})
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := c.PutSecret(context.Background(), ScopePersonal, "", "A_B", "s3cret", exp); err != nil {
		t.Fatal(err)
	}
	if err := c.PutSecret(context.Background(), ScopeTeam, "ws-1", "T_K", "v2", time.Time{}); err != nil {
		t.Fatal(err)
	}
	got := *seen
	if got[0].Method != "PUT" || got[0].Path != "/api/v1/secrets/A_B" || got[0].Auth != "Bearer cli-token" {
		t.Errorf("personal put = %+v", got[0])
	}
	var b map[string]string
	_ = json.Unmarshal([]byte(got[0].Body), &b)
	if b["value"] != "s3cret" || b["expires_at"] != "2027-01-02T03:04:05Z" {
		t.Errorf("personal body = %v", b)
	}
	if got[1].Path != "/api/v1/workspaces/ws-1/secrets/T_K" || strings.Contains(got[1].Body, "expires_at") {
		t.Errorf("team put = %+v", got[1])
	}
	if err := c.PutSecret(context.Background(), ScopeTeam, "", "T_K", "v", time.Time{}); err == nil {
		t.Error("a team secret with no workspace must be refused before any request")
	}
}

func TestGrantAndListSecrets(t *testing.T) {
	c, seen := secretsServer(t,
		[2]string{"204", ""},
		[2]string{"", `[{"key":"A_B","scope":"personal","has_value":true,"readers":["rmt_1"],"expires_at":"2027-01-02T03:04:05Z","expired":false}]`})
	if err := c.GrantSecret(context.Background(), ScopePersonal, "", "A_B", "rmt_1"); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0]; got.Method != "PUT" || got.Path != "/api/v1/secrets/A_B/grants/rmt_1" {
		t.Errorf("grant = %+v", got)
	}
	metas, err := c.ListSecrets(context.Background(), ScopePersonal, "")
	if err != nil || len(metas) != 1 {
		t.Fatalf("list = %v, %v", metas, err)
	}
	if at, ok := metas[0].Expiry(); !ok || !at.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("Expiry = %v, %v", at, ok)
	}
	// A listing has no field that could hold a value.
	raw, _ := json.Marshal(metas[0])
	if strings.Contains(string(raw), "value") && !strings.Contains(string(raw), "has_value") {
		t.Errorf("SecretMeta marshals a value: %s", raw)
	}
}

// A refusal is the server's {error, reason}, not a trigger rejection.
func TestRefusalIsATypedAPIError(t *testing.T) {
	c, _ := secretsServer(t, [2]string{"403", `{"error":"refused","reason":"personal_rules"}`})
	err := c.GrantSecret(context.Background(), ScopePersonal, "", "A_B", "rmt_1")
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T %v", err, err)
	}
	if ae.Status != 403 || ae.Code != "refused" || ae.Reason != "personal_rules" {
		t.Errorf("APIError = %+v", ae)
	}
	var tr *TriggerRefusedError
	if errors.As(err, &tr) {
		t.Error("a secret refusal must not read as a trigger rejection")
	}
	if got := err.Error(); got != "refused: personal_rules" {
		t.Errorf("Error() = %q", got)
	}
}

func TestSetRemoteKeyAndIdentityAndRun(t *testing.T) {
	c, seen := secretsServer(t,
		[2]string{"", `{"fingerprint":"SHA256:abc","set_at":"2026-10-20T00:00:00Z"}`},
		[2]string{"", `{}`},
		[2]string{"204", ""},
		[2]string{"201", `{"run_id":"trg_9","expires_at":"2026-10-20T00:30:00Z"}`})
	ctx := context.Background()
	info, err := c.SetRemoteKey(ctx, "rmt_1", "cHVi")
	if err != nil || info.Fingerprint != "SHA256:abc" {
		t.Fatalf("SetRemoteKey = %+v, %v", info, err)
	}
	if err := c.SetRemoteIdentity(ctx, "rmt_1", "https://oidc.example", "org:app:*"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRemoteIdentity(ctx, "rmt_1"); err != nil {
		t.Fatal(err)
	}
	run, err := c.CreateRun(ctx, "rmt_1")
	if err != nil || run.RunID != "trg_9" {
		t.Fatalf("CreateRun = %+v, %v", run, err)
	}
	got := *seen
	wantPaths := []string{"/api/v1/remotes/rmt_1/key", "/api/v1/remotes/rmt_1/identity", "/api/v1/remotes/rmt_1/identity", "/api/v1/remotes/rmt_1/runs"}
	wantMethods := []string{"PUT", "PUT", "DELETE", "POST"}
	for i := range wantPaths {
		if got[i].Path != wantPaths[i] || got[i].Method != wantMethods[i] {
			t.Errorf("call %d = %s %s", i, got[i].Method, got[i].Path)
		}
	}
	if !strings.Contains(got[0].Body, `"public_key":"cHVi"`) || !strings.Contains(got[1].Body, `"issuer":"https://oidc.example"`) {
		t.Errorf("bodies: %q %q", got[0].Body, got[1].Body)
	}
}

// authedCall and SignedCall build their error from the one parser of the body.
func TestCallsShareTheOneErrorParser(t *testing.T) {
	const body = `{"error":"refused","reason":"no_grant","message":"ignored when reason is set"}`
	want, ok := decodeAPIError([]byte(body))
	if !ok {
		t.Fatal("decodeAPIError rejected the body")
	}
	c, _ := secretsServer(t, [2]string{"403", body})

	_, aerr := c.authedCall(context.Background(), http.MethodGet, "/x", nil)
	_, serr := c.SignedCall(context.Background(), SignedRequest{Method: http.MethodPost, Path: "/x", Header: map[string]string{"X-Satelle-Signature": "s"}})
	for name, err := range map[string]error{"authedCall": aerr, "SignedCall": serr} {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Code != want.Error || ae.Reason != want.Reason {
			t.Errorf("%s: %+v vs parser %+v", name, ae, want)
		}
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
