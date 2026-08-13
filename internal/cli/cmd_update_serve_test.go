package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
		art         artifactIdentity
		want        serveOutcome
	}{
		{"already current (version+artifact)", "0.0.12", "serve-v0.0.12", nil, true, artifactMatch, serveCurrent},
		{"version match but artifact differs", "0.0.12", "serve-v0.0.12", nil, true, artifactDiffer, serveInstall},
		{"version match but artifact unverified", "0.0.12", "serve-v0.0.12", nil, true, artifactUnknown, serveUnverified},
		{"newer release available", "0.0.11", "serve-v0.0.12", nil, true, artifactMatch, serveInstall},
		{"binary absent, release exists", "", "serve-v0.0.12", nil, false, artifactUnknown, serveInstall},
		{"version unreadable is never current", "", "serve-v0.0.12", nil, true, artifactMatch, serveInstall},
		{"unresolvable with a binary installed fails", "0.0.11", "", transport, true, artifactUnknown, serveFail},
		{"transport error fails even with no binary", "", "", transport, false, artifactUnknown, serveFail},
		{"no release and no binary is a no-op", "", "", notPublished, false, artifactUnknown, serveAbsentNoRelease},
		{"no release but a binary is installed fails", "0.0.11", "", notPublished, true, artifactUnknown, serveFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyServeOutcome(tc.installed, tc.tag, tc.err, tc.servePresen, tc.art); got != tc.want {
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

	missing := filepath.Join(dir, "satelled")
	if v, present := serveInstalledVersion(missing); present || v != "" {
		t.Fatalf("absent binary reported version=%q present=%v", v, present)
	}

	if runtime := os.Getenv("GOOS"); runtime == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	stub := filepath.Join(dir, "stub-serve")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'satelled 0.0.12 (commit abc, built now)'\n"), 0o755); err != nil {
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

// TestServeRetagReplacesSameVersion is the AC4 reproduction for the serve half
// (sty_1cd2ff01): install serve asset A at serve-vX, republish same tag as B;
// update must replace rather than report "already up to date".
func TestServeRetagReplacesSameVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	resetUpdateFlags(t)
	t.Cleanup(func() { resetUpdateFlags(t) })
	installDir := t.TempDir()
	t.Setenv("SATELLE_INSTALL_DIR", installDir)

	// CLI half: install a matching fake CLI so update does not try to replace it
	// from a missing asset. We only care about the serve half.
	cliScript := []byte("#!/bin/sh\necho 'satelle 9.9.9 (commit cli, built t)'\n")
	cliSum := sha256.Sum256(cliScript)
	cliName := assetNameFor("satelle", "v9.9.9")
	if err := os.WriteFile(filepath.Join(installDir, "satelle"), cliScript, 0o755); err != nil {
		t.Fatal(err)
	}

	scriptA := []byte("#!/bin/sh\necho 'satelled 0.0.17 (commit aaa, built t)'\n")
	scriptB := []byte("#!/bin/sh\necho 'satelled 0.0.17 (commit bbb, built t)'\n")
	sumA := sha256.Sum256(scriptA)
	sumB := sha256.Sum256(scriptB)
	serveName := assetNameFor("satelled", "serve-v0.0.17")

	which := "A"
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"serve-v0.0.17","draft":false},{"tag_name":"v9.9.9","draft":false}]`)
	})
	mux.HandleFunc("/download/v9.9.9/"+cliName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(cliScript)
	})
	mux.HandleFunc("/download/v9.9.9/"+cliName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(cliSum[:]), cliName)
	})
	mux.HandleFunc("/download/serve-v0.0.17/"+serveName, func(w http.ResponseWriter, r *http.Request) {
		if which == "A" {
			w.Write(scriptA)
		} else {
			w.Write(scriptB)
		}
	})
	mux.HandleFunc("/download/serve-v0.0.17/"+serveName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		sum := sumA
		if which != "A" {
			sum = sumB
		}
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), serveName)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("SATELLE_RELEASE_API", srv.URL+"/releases/latest")
	t.Setenv("SATELLE_RELEASE_LIST_API", srv.URL+"/releases")
	t.Setenv("SATELLE_RELEASE_BASE", srv.URL+"/download")

	serveTarget := filepath.Join(installDir, "satelled")
	if err := os.WriteFile(serveTarget, scriptA, 0o755); err != nil {
		t.Fatal(err)
	}

	out1, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update (serve match): %v\n%s", err, out1)
	}
	if !strings.Contains(out1, "satelled already up to date") {
		t.Fatalf("expected serve match message:\n%s", out1)
	}

	which = "B"
	out2, err := runRoot(t, "update", "--no-restart")
	if err != nil {
		t.Fatalf("update (serve retag): %v\n%s", err, out2)
	}
	if strings.Contains(out2, "already up to date") && strings.Contains(out2, "satelled") {
		// The CLI half may still say up to date; the serve half must not.
		if strings.Contains(out2, "satelled already up to date") {
			t.Fatalf("serve retag must not claim up to date:\n%s", out2)
		}
	}
	if !strings.Contains(out2, "differs from published") {
		t.Fatalf("expected serve differ message:\n%s", out2)
	}
	got, _ := os.ReadFile(serveTarget)
	if string(got) != string(scriptB) {
		t.Fatalf("serve bytes not replaced with B: %q", got)
	}
}
