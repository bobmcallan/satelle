package hosted

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const unknownSubjectMessage = "Sign in at https://satelle.dev first, then retry."

// meServer is the satelle server for the refresh tests: /api/v1/me accepts one
// bearer (swappable), and every call to /oauth/token or /me is counted. A
// request to /oauth/token fails the test: the CLI refreshes at the provider.
type meServer struct {
	ts       *httptest.Server
	accept   atomic.Value // string: the bearer /me accepts
	meHits   atomic.Int32
	tokenHit atomic.Int32
	// on401 writes the 401 body; nil writes an empty one.
	on401 func(w http.ResponseWriter)
}

func newMeServer(t *testing.T, accept string) *meServer {
	t.Helper()
	s := &meServer{}
	s.accept.Store(accept)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		s.meHits.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+s.accept.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			if s.on401 != nil {
				s.on401(w)
			}
			return
		}
		_ = json.NewEncoder(w).Encode(Principal{ID: "u1", Email: "a@b.c"})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		s.tokenHit.Add(1)
		t.Errorf("satelle server must not receive a token request")
	})
	s.ts = httptest.NewServer(mux)
	t.Cleanup(s.ts.Close)
	requireLoopback(t, s.ts.URL)
	return s
}

// AC4 (REST): the refresh goes to the provider token endpoint stored with the
// credential and satelle-server sees no /oauth/token call.
func TestRESTRefreshGoesToProvider(t *testing.T) {
	prov := newFakeProvider(t)
	srv := newMeServer(t, "access-2")
	store := &memStore{}
	_ = store.Save(prov.credential(srv.ts.URL))

	if _, err := NewClient(srv.ts.URL, store, guardedClient(t)).Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if n := len(prov.refreshes()); n != 1 {
		t.Fatalf("provider refreshes = %d, want 1", n)
	}
	prov.wantRefresh(t, 0, "refresh-1")
	if srv.tokenHit.Load() != 0 {
		t.Errorf("satelle server received %d /oauth/token calls", srv.tokenHit.Load())
	}
	if srv.meHits.Load() != 2 {
		t.Errorf("Me hits = %d, want 2 (401 then retry)", srv.meHits.Load())
	}
}

// AC4: the second refresh still reaches the provider with the same client id
// and resource — rotation carries the discovery fields over.
func TestRESTSecondRefreshStillReachesProvider(t *testing.T) {
	prov := newFakeProvider(t)
	srv := newMeServer(t, "access-2")
	store := &memStore{}
	_ = store.Save(prov.credential(srv.ts.URL))
	c := NewClient(srv.ts.URL, store, guardedClient(t))

	if _, err := c.Me(context.Background()); err != nil {
		t.Fatalf("first Me: %v", err)
	}
	srv.accept.Store("access-3") // the access token expires again
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatalf("second Me: %v", err)
	}
	if n := len(prov.refreshes()); n != 2 {
		t.Fatalf("provider refreshes = %d, want 2", n)
	}
	prov.wantRefresh(t, 0, "refresh-1")
	prov.wantRefresh(t, 1, "refresh-2")
	got, _ := store.Load(srv.ts.URL)
	if got.TokenEndpoint != prov.tokenURL() || got.ClientID != "client-abc" || got.Resource != "https://api.example/" {
		t.Errorf("discovery fields lost after two rotations: %+v", got)
	}
	if srv.tokenHit.Load() != 0 {
		t.Errorf("satelle server received %d /oauth/token calls", srv.tokenHit.Load())
	}
}

// A credential from before discovery has no token endpoint: login required, no
// request to anyone.
func TestRESTCredentialWithoutTokenEndpointIsLoginRequired(t *testing.T) {
	srv := newMeServer(t, "never")
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: srv.ts.URL, AccessToken: "access-1", RefreshToken: "refresh-1"})
	_, err := NewClient(srv.ts.URL, store, guardedClient(t)).Me(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
	if srv.meHits.Load() != 1 || srv.tokenHit.Load() != 0 {
		t.Errorf("Me hits = %d, token hits = %d; want 1 and 0", srv.meHits.Load(), srv.tokenHit.Load())
	}
}

// AC6 (REST): a 401 whose JSON error is unknown_subject is shown with the
// server's message, with exactly one request and no refresh.
func TestRESTUnknownSubjectIsShownNotRetried(t *testing.T) {
	prov := newFakeProvider(t)
	srv := newMeServer(t, "never")
	srv.on401 = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown_subject", "message": unknownSubjectMessage})
	}
	store := &memStore{}
	_ = store.Save(prov.credential(srv.ts.URL))

	_, err := NewClient(srv.ts.URL, store, guardedClient(t)).Me(context.Background())
	var us *UnknownSubjectError
	if !errors.As(err, &us) || us.Message != unknownSubjectMessage {
		t.Fatalf("err = %v, want UnknownSubjectError carrying the server's message", err)
	}
	if !strings.Contains(err.Error(), unknownSubjectMessage) {
		t.Errorf("message not in error text: %v", err)
	}
	if errors.Is(err, ErrLoginRequired) {
		t.Errorf("an unknown subject must not tell the user to run satelle login: %v", err)
	}
	if srv.meHits.Load() != 1 {
		t.Errorf("requests reaching the server = %d, want exactly 1", srv.meHits.Load())
	}
	if n := len(prov.refreshes()); n != 0 {
		t.Errorf("provider refreshes = %d, want 0", n)
	}
}

// The legacy body names the sign-up site but is not unknown_subject: it takes
// the normal refresh path.
func TestRESTOtherUnauthorizedBodyStillRefreshes(t *testing.T) {
	prov := newFakeProvider(t)
	srv := newMeServer(t, "access-2")
	srv.on401 = func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "sign in at satelle.dev first"})
	}
	store := &memStore{}
	_ = store.Save(prov.credential(srv.ts.URL))

	if _, err := NewClient(srv.ts.URL, store, guardedClient(t)).Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if n := len(prov.refreshes()); n != 1 || srv.meHits.Load() != 2 {
		t.Fatalf("provider refreshes = %d, Me hits = %d; want 1 and 2", n, srv.meHits.Load())
	}
}

func TestDecodeAPIErrorIsTheOneParser(t *testing.T) {
	if e, ok := decodeAPIError([]byte(`{"error":"e","message":"m"}`)); !ok || e.Error != "e" || e.Message != "m" {
		t.Errorf("decodeAPIError = %+v, %v", e, ok)
	}
	if _, ok := decodeAPIError([]byte(`not json`)); ok {
		t.Error("non-JSON body must not decode")
	}
	if got := serverError(409, []byte(`{"message":"clash"}`)); got != "HTTP 409: clash" {
		t.Errorf("serverError = %q", got)
	}
	if unknownSubject([]byte(`{"error":"unknown_subject"}`)) == nil || unknownSubject([]byte(`{"error":"unknown_subject_x"}`)) != nil {
		t.Error("unknownSubject must match the code exactly")
	}
}

func jwtWithPayload(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestCredentialSessionID(t *testing.T) {
	cases := map[string]struct {
		token string
		want  string
	}{
		"sid claim":          {jwtWithPayload(`{"sub":"u","sid":"session_01ABC"}`), "session_01ABC"},
		"no sid":             {jwtWithPayload(`{"sub":"u"}`), ""},
		"opaque token":       {"acc", ""},
		"bad base64 payload": {"a.!!!.c", ""},
		"payload not JSON":   {jwtWithPayload(`nope`), ""},
		"empty":              {"", ""},
	}
	for name, tc := range cases {
		if got := (Credential{AccessToken: tc.token}).SessionID(); got != tc.want {
			t.Errorf("%s: SessionID = %q, want %q", name, got, tc.want)
		}
	}
}

// AC2: the binary holds nothing provider-specific. The provider is learned from
// discovery, so no WorkOS/AuthKit host, issuer or client id literal (including
// the old satelle-cli id) may sit in the non-test sources of the login path.
func TestNoHardcodedProviderLiterals(t *testing.T) {
	banned := []string{"workos", "authkit", `"satelle-cli"`}
	for _, dir := range []string{".", filepath.Join("..", "cli")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src := strings.ToLower(string(b))
			for _, w := range banned {
				if strings.Contains(src, w) {
					t.Errorf("%s names %s: the provider must come from discovery", f, w)
				}
			}
		}
	}
}
