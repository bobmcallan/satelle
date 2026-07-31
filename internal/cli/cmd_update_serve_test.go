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
	"testing"
)

// TestServeReleaseFoundBeyondFirstPage proves AC1 of sty_0dcedb0d against the
// exact payload shape that failed in production: the newest serve release has
// been pushed off the first page by a run of CLI releases. Discovery must walk
// to it rather than report that no serve release exists.
func TestServeReleaseFoundBeyondFirstPage(t *testing.T) {
	var pagesFetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pagesFetched = append(pagesFetched, page)
		if got := r.URL.Query().Get("per_page"); got != strconv.Itoa(releasePageSize) {
			t.Errorf("per_page = %q, want %d", got, releasePageSize)
		}
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			// A full page of CLI-only releases — exactly what a busy CLI cadence
			// puts in front of a rare serve release.
			var entries []string
			for i := 0; i < releasePageSize; i++ {
				entries = append(entries, fmt.Sprintf(`{"tag_name":"v0.0.%d","draft":false}`, 400-i))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(entries, ","))
		case "2":
			fmt.Fprint(w, `[{"tag_name":"v0.0.300","draft":false},{"tag_name":"serve-v0.0.11","draft":false}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()
	t.Setenv("SATELLE_RELEASE_LIST_API", srv.URL)

	got, err := latestServeReleaseTag(context.Background(), updateRepo)
	if err != nil {
		t.Fatalf("latestServeReleaseTag: %v", err)
	}
	if got != "serve-v0.0.11" {
		t.Fatalf("got %q, want serve-v0.0.11", got)
	}
	if len(pagesFetched) != 2 || pagesFetched[0] != "1" || pagesFetched[1] != "2" {
		t.Fatalf("pages fetched = %v, want exactly pages 1 then 2", pagesFetched)
	}
}

// TestServeReleaseWalkStopsAndReportsHonestly covers the walk's terminating
// cases: a short page ends the list, a draft is never selected, and exhausting
// the page cap is reported distinguishably from an exhausted list — "not found
// in the newest N" must not read as "does not exist".
func TestServeReleaseWalkStopsAndReportsHonestly(t *testing.T) {
	fullCLIPage := func() []byte {
		var entries []string
		for i := 0; i < releasePageSize; i++ {
			entries = append(entries, fmt.Sprintf(`{"tag_name":"v0.0.%d","draft":false}`, 900-i))
		}
		return []byte("[" + strings.Join(entries, ",") + "]")
	}

	t.Run("short page ends the list", func(t *testing.T) {
		fetched := 0
		_, err := firstPrefixedTagInPages(func(page int) ([]byte, error) {
			fetched++
			return []byte(`[{"tag_name":"v0.0.1","draft":false}]`), nil
		}, "serve-v", 10)
		if err == nil || !strings.Contains(err.Error(), "no published release with prefix") {
			t.Fatalf("err = %v, want a plain not-published error", err)
		}
		if fetched != 1 {
			t.Fatalf("fetched %d pages, want 1 — a short page is the end of the list", fetched)
		}
	})

	t.Run("draft is skipped", func(t *testing.T) {
		got, err := firstPrefixedTagInPages(func(page int) ([]byte, error) {
			return []byte(`[{"tag_name":"serve-v0.0.99","draft":true},{"tag_name":"serve-v0.0.12","draft":false}]`), nil
		}, "serve-v", 10)
		if err != nil || got != "serve-v0.0.12" {
			t.Fatalf("got %q err %v — a draft carries no asset and must not be selected", got, err)
		}
	})

	t.Run("cap exhausted says so", func(t *testing.T) {
		fetched := 0
		_, err := firstPrefixedTagInPages(func(page int) ([]byte, error) {
			fetched++
			return fullCLIPage(), nil
		}, "serve-v", 3)
		if err == nil || !strings.Contains(err.Error(), "in the newest") {
			t.Fatalf("err = %v, want the searched-N-releases wording", err)
		}
		if fetched != 3 {
			t.Fatalf("fetched %d pages, want the 3-page cap", fetched)
		}
	})

	t.Run("fetch error propagates", func(t *testing.T) {
		_, err := firstPrefixedTagInPages(func(page int) ([]byte, error) {
			return nil, errors.New("boom")
		}, "serve-v", 10)
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want the transport error", err)
		}
	})
}

// TestReleaseListPageURL proves pagination merges with an override URL that
// already carries a query, rather than corrupting it.
func TestReleaseListPageURL(t *testing.T) {
	got := releaseListPageURL("https://example.test/releases?foo=bar", 2)
	if !strings.Contains(got, "foo=bar") || !strings.Contains(got, "page=2") ||
		!strings.Contains(got, "per_page="+strconv.Itoa(releasePageSize)) {
		t.Fatalf("url = %q, want the original query plus per_page and page", got)
	}
}

// TestClassifyServeOutcome proves AC2: the three serve outcomes are distinct.
// They used to collapse into one "skipped" line at exit 0, which is how a
// release reported success while the live service stayed on an older binary.
func TestClassifyServeOutcome(t *testing.T) {
	notPublished := fmt.Errorf("no published release with prefix %q", "serve-v")
	transport := errors.New("https://api.github.com/…: 503 Service Unavailable")

	cases := []struct {
		name        string
		installed   string
		tag         string
		err         error
		servePresen bool
		want        serveOutcome
	}{
		{"already current", "0.0.12", "serve-v0.0.12", nil, true, serveCurrent},
		{"newer release available", "0.0.11", "serve-v0.0.12", nil, true, serveInstall},
		{"binary absent, release exists", "", "serve-v0.0.12", nil, false, serveInstall},
		{"version unreadable is never current", "", "serve-v0.0.12", nil, true, serveInstall},
		{"unresolvable with a binary installed fails", "0.0.11", "", transport, true, serveFail},
		{"transport error fails even with no binary", "", "", transport, false, serveFail},
		{"no release and no binary is a no-op", "", "", notPublished, false, serveAbsentNoRelease},
		{"no release but a binary is installed fails", "0.0.11", "", notPublished, true, serveFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyServeOutcome(tc.installed, tc.tag, tc.err, tc.servePresen); got != tc.want {
				t.Fatalf("classifyServeOutcome = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestServeInstalledVersion proves the installed-version probe: a real binary is
// read, an absent one reports absent, and an unreadable one reports present with
// an unknown version (which classifies as install, never as current).
func TestServeInstalledVersion(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "satelle-serve")
	if v, present := serveInstalledVersion(missing); present || v != "" {
		t.Fatalf("absent binary reported version=%q present=%v", v, present)
	}

	if runtime := os.Getenv("GOOS"); runtime == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	stub := filepath.Join(dir, "stub-serve")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'satelle-serve 0.0.12 (commit abc, built now)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	v, present := serveInstalledVersion(stub)
	if !present || v != "0.0.12" {
		t.Fatalf("version=%q present=%v, want 0.0.12/true", v, present)
	}

	broken := filepath.Join(dir, "broken-serve")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v, present := serveInstalledVersion(broken); !present || v != "" {
		t.Fatalf("unrunnable binary reported version=%q present=%v, want \"\"/true", v, present)
	}
}
