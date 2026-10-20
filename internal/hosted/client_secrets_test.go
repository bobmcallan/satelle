package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted/secretstest"
)

func secretsClient(t *testing.T) (*Client, *secretstest.Server) {
	t.Helper()
	srv := secretstest.New(t)
	store := &memStore{cred: Credential{ServerURL: srv.URL(), AccessToken: "owner-token", RefreshToken: "r"}, set: true}
	return NewClient(srv.URL(), store, nil), srv
}

// AC1: a put sends the value and the personal flag in the body to the key's
// route, and the list that follows carries every attribute but a value.
func TestPutAndListSecretsNeverReturnAValue(t *testing.T) {
	c, srv := secretsClient(t)
	ctx := context.Background()
	if err := c.PutSecret(ctx, "API_TOKEN", "planted-value-1", false); err != nil {
		t.Fatal(err)
	}
	if err := c.PutSecret(ctx, "SUB_TOKEN", "planted-value-2", true); err != nil {
		t.Fatal(err)
	}

	reqs := srv.Requests()
	if reqs[0].Method != http.MethodPut || reqs[0].Path != "/api/v1/secrets/API_TOKEN" || reqs[0].Bearer != "Bearer owner-token" {
		t.Errorf("first request = %+v", reqs[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(reqs[1].Body), &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, map[string]any{"value": "planted-value-2", "personal": true}) {
		t.Errorf("put body = %v", body)
	}

	list, err := c.ListSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []SecretMeta{
		{Key: "API_TOKEN", HasValue: true, Readers: []string{}},
		{Key: "SUB_TOKEN", HasValue: true, Personal: true, Readers: []string{}},
	}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("list = %+v, want %+v", list, want)
	}
	out, _ := json.Marshal(list)
	if strings.Contains(string(out), "planted-value") {
		t.Errorf("the listing carries a value: %s", out)
	}
}

// AC2: grant and revoke call the grants route with the remote in the path.
func TestGrantAndRevokeSecret(t *testing.T) {
	c, srv := secretsClient(t)
	ctx := context.Background()
	_ = c.PutSecret(ctx, "API_TOKEN", "v", false)
	if err := c.GrantSecret(ctx, "API_TOKEN", "edge", true); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	g := reqs[len(reqs)-1]
	if g.Method != http.MethodPut || g.Path != "/api/v1/secrets/API_TOKEN/grants/edge" || g.Body != `{"personal":true}` {
		t.Errorf("grant request = %+v", g)
	}
	list, _ := c.ListSecrets(ctx)
	if !reflect.DeepEqual(list[0].Readers, []string{"edge"}) || !list[0].Personal {
		t.Errorf("after grant = %+v", list[0])
	}

	if err := c.RevokeSecret(ctx, "API_TOKEN", "edge"); err != nil {
		t.Fatal(err)
	}
	reqs = srv.Requests()
	if r := reqs[len(reqs)-1]; r.Method != http.MethodDelete || r.Path != "/api/v1/secrets/API_TOKEN/grants/edge" {
		t.Errorf("revoke request = %+v", r)
	}
	list, _ = c.ListSecrets(ctx)
	if len(list[0].Readers) != 0 {
		t.Errorf("after revoke readers = %v", list[0].Readers)
	}

	var refused *SecretRefusedError
	if err := c.GrantSecret(ctx, "NO_SUCH", "edge", false); !errors.As(err, &refused) {
		t.Errorf("granting an unknown key = %v, want a refusal", err)
	}
}

// AC7/AC9: a run is registered with the wake key, read within its grant, and a
// refusal carries the server's reason; a replayed nonce and an ended run refuse.
func TestRunLifecycleAndReadRefusals(t *testing.T) {
	c, srv := secretsClient(t)
	ctx := context.Background()
	_ = c.PutSecret(ctx, "API_TOKEN", "the-value", false)
	_ = c.PutSecret(ctx, "SUB_TOKEN", "sub-value", true)
	_ = c.GrantSecret(ctx, "API_TOKEN", "edge", false)
	_ = c.GrantSecret(ctx, "SUB_TOKEN", "edge", true)
	srv.MarkFireable("edge")

	run, err := c.RegisterRun(ctx, "edge", "MY_WAKER")
	if err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if r := reqs[len(reqs)-1]; r.Path != "/api/v1/remotes/edge/runs" || r.Body != `{"wake_key":"MY_WAKER"}` {
		t.Errorf("register request = %+v", r)
	}
	if err := c.RefreshRun(ctx, "edge", run); err != nil || srv.Refreshes(run) != 1 {
		t.Errorf("refresh: err=%v refreshes=%d", err, srv.Refreshes(run))
	}

	v, err := c.ReadSecret(ctx, "edge", run, "API_TOKEN", "n1")
	if err != nil || v != "the-value" {
		t.Fatalf("read = %q, %v", v, err)
	}
	reqs = srv.Requests()
	if r := reqs[len(reqs)-1]; r.Path != "/api/v1/remotes/edge/runs/"+run+"/secrets/API_TOKEN:read" || r.Body != `{"nonce":"n1"}` {
		t.Errorf("read request = %+v", r)
	}

	for _, tc := range []struct{ name, key, nonce, reason string }{
		{"replay", "API_TOKEN", "n1", "already used"},
		{"personal on a fireable remote", "SUB_TOKEN", "n2", "personal key"},
		{"no grant", "OTHER", "n3", "no grant"},
	} {
		_, err := c.ReadSecret(ctx, "edge", run, tc.key, tc.nonce)
		var refused *SecretRefusedError
		if !errors.As(err, &refused) || !strings.Contains(refused.Reason, tc.reason) {
			t.Errorf("%s: err = %v, want a refusal containing %q", tc.name, err, tc.reason)
		}
	}

	if err := c.EndRun(ctx, "edge", run); err != nil || srv.Live(run) {
		t.Errorf("end: err=%v live=%v", err, srv.Live(run))
	}
	if _, err := c.ReadSecret(ctx, "edge", run, "API_TOKEN", "n4"); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Errorf("read after end = %v, want the not-live refusal", err)
	}
}

// A refusal body without a "reason" falls back to the server's message, then code.
func TestRefusalReasonFallsBack(t *testing.T) {
	for body, want := range map[string]string{
		`{"reason":"r","message":"m","error":"e"}`: "r",
		`{"message":"m","error":"e"}`:              "m",
		`{"error":"e"}`:                            "e",
		`not json`:                                 "HTTP 403",
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(body))
		}))
		c := NewClient(ts.URL, &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "t"}, set: true}, ts.Client())
		_, err := c.ReadSecret(context.Background(), "edge", "run-1", "K", "n")
		var refused *SecretRefusedError
		if !errors.As(err, &refused) || refused.Reason != want {
			t.Errorf("body %s: err = %v, want reason %q", body, err, want)
		}
		ts.Close()
	}
}

func TestSecretCallsNeedASignIn(t *testing.T) {
	c := NewClient("https://example", &memStore{}, nil)
	if err := c.PutSecret(context.Background(), "K", "v", false); !errors.Is(err, ErrLoginRequired) {
		t.Errorf("err = %v, want ErrLoginRequired", err)
	}
}
