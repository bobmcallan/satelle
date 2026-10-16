package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/testutil"
)

// authRecorder is a release API stub that records the Authorization header of
// each request, keyed by path.
type authRecorder struct {
	mu   sync.Mutex
	seen map[string][]string
}

func (a *authRecorder) record(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen = map[string][]string{}
	}
	a.seen[r.URL.Path] = append(a.seen[r.URL.Path], r.Header.Get("Authorization"))
}

func (a *authRecorder) headers(path string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen[path]...)
}

// releaseAPIStub serves the latest and list endpoints through h and points the
// release overrides at it. The install dir is empty and the home is isolated.
func releaseAPIStub(t *testing.T, h http.HandlerFunc) (home string) {
	t.Helper()
	resetUpdateFlags(t)
	t.Cleanup(func() { resetUpdateFlags(t) })
	home = testutil.IsolateHome(t)
	t.Setenv("SATELLE_INSTALL_DIR", t.TempDir())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("SATELLE_RELEASE_API", srv.URL+"/releases/latest")
	t.Setenv("SATELLE_RELEASE_LIST_API", srv.URL+"/releases")
	t.Setenv("SATELLE_RELEASE_BASE", srv.URL+"/download")
	return home
}

func okReleaseHandler(rec *authRecorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v0.0.99"}`)
		case "/releases":
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestReleaseAPISendsToken(t *testing.T) {
	cases := []struct {
		name, ghToken, githubToken, want string
	}{
		{"GH_TOKEN only", "gh-tok", "", "Bearer gh-tok"},
		{"GITHUB_TOKEN only", "", "github-tok", "Bearer github-tok"},
		{"GH_TOKEN wins", "gh-tok", "github-tok", "Bearer gh-tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &authRecorder{}
			releaseAPIStub(t, okReleaseHandler(rec))
			t.Setenv("GH_TOKEN", tc.ghToken)
			t.Setenv("GITHUB_TOKEN", tc.githubToken)

			if out, err := runRoot(t, "update", "--check"); err != nil {
				t.Fatalf("update --check: %v\n%s", err, out)
			}
			for _, path := range []string{"/releases/latest", "/releases"} {
				got := rec.headers(path)
				if len(got) == 0 {
					t.Fatalf("%s was never requested", path)
				}
				for _, h := range got {
					if h != tc.want {
						t.Errorf("%s Authorization = %q, want %q", path, h, tc.want)
					}
				}
			}
		})
	}
}

func TestReleaseAPINoTokenIsAnonymous(t *testing.T) {
	for _, blank := range []string{"", "   "} {
		t.Run(strconv.Quote(blank), func(t *testing.T) {
			rec := &authRecorder{}
			releaseAPIStub(t, okReleaseHandler(rec))
			t.Setenv("GH_TOKEN", blank)
			t.Setenv("GITHUB_TOKEN", blank)

			if out, err := runRoot(t, "update", "--check"); err != nil {
				t.Fatalf("update --check: %v\n%s", err, out)
			}
			for _, path := range []string{"/releases/latest", "/releases"} {
				got := rec.headers(path)
				if len(got) == 0 {
					t.Fatalf("%s was never requested", path)
				}
				for _, h := range got {
					if h != "" {
						t.Errorf("%s sent Authorization %q with no token set", path, h)
					}
				}
			}
		})
	}
}

// rateLimitHandler answers the latest endpoint with a rate-limit 403.
func rateLimitHandler(reset time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
	}
}

func TestGitHubAPIRateLimitHint(t *testing.T) {
	reset := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	wantReset := reset.Local().Format(time.RFC3339)

	t.Run("unauthenticated names the limit, the reset and the remedy", func(t *testing.T) {
		releaseAPIStub(t, rateLimitHandler(reset))
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		out, err := runRoot(t, "update", "--check")
		if err == nil {
			t.Fatalf("want an error, got output:\n%s", out)
		}
		msg := err.Error()
		for _, want := range []string{"GitHub API rate limit exceeded", wantReset, "GH_TOKEN", "GITHUB_TOKEN"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error lacks %q: %s", want, msg)
			}
		}
		if strings.Contains(msg, "403 Forbidden") && !strings.Contains(msg, "rate limited") {
			t.Errorf("reported as a bare 403: %s", msg)
		}
		var rl *rateLimitError
		if !errors.As(err, &rl) {
			t.Errorf("the typed rate limit error is lost from the chain: %v", err)
		}
	})

	t.Run("authenticated keeps the reset and drops the remedy", func(t *testing.T) {
		releaseAPIStub(t, rateLimitHandler(reset))
		t.Setenv("GH_TOKEN", "authed-token")
		t.Setenv("GITHUB_TOKEN", "")
		_, err := latestReleaseTag(context.Background(), updateRepo)
		if err == nil {
			t.Fatal("want an error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "GitHub API rate limit exceeded") || !strings.Contains(msg, wantReset) {
			t.Errorf("error lacks the limit or the reset: %s", msg)
		}
		if strings.Contains(msg, "GH_TOKEN") || strings.Contains(msg, "GITHUB_TOKEN") {
			t.Errorf("a token was sent, yet the error asks for one: %s", msg)
		}
	})
}

// TestTokenNeverLeaks runs the check against a healthy, a rate-limited and a
// failing release API with a token set, then greps what satelle printed, the
// errors it returned and every file it wrote.
func TestTokenNeverLeaks(t *testing.T) {
	token := "satelle-test-token-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	reset := time.Now().Add(10 * time.Minute)

	handlers := map[string]http.HandlerFunc{
		"ok":           okReleaseHandler(&authRecorder{}),
		"rate limited": rateLimitHandler(reset),
		"server error": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			home := releaseAPIStub(t, h)
			t.Setenv("GH_TOKEN", token)
			t.Setenv("GITHUB_TOKEN", "")

			out, err := runRoot(t, "update", "--check")
			if strings.Contains(out, token) {
				t.Errorf("output carries the token:\n%s", out)
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Errorf("error carries the token: %v", err)
			}
			installDir := os.Getenv("SATELLE_INSTALL_DIR")
			for _, root := range []string{home, installDir} {
				_ = filepath.Walk(root, func(p string, info os.FileInfo, werr error) error {
					if werr != nil || info.IsDir() {
						return nil
					}
					if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), token) {
						t.Errorf("%s carries the token", p)
					}
					return nil
				})
			}
		})
	}
}
