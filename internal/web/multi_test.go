package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"satelle-homepage": "satelle-homepage",
		"My Repo!":         "my-repo",
		"  spaced  ":       "spaced",
		"a/b\\c":           "a-b-c",
		"":                 "project",
		"___":              "project",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectsPageListsBoundAndChildren(t *testing.T) {
	projects := []Project{
		{Slug: "satelle", Name: "satelle", Path: "/repos/satelle"},
		{Slug: "satelle-homepage", Name: "satelle-homepage", Path: "/repos/satelle-homepage"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ProjectsPage(w, r, projects, []FailedProject{{Name: "broken", Path: "/repos/broken", Err: "spawn: exec failed"}})
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	for _, want := range []string{
		"connected project",                 // landing header
		`href="/satelle/#stories"`,          // launch repo under its own slug now
		`href="/satelle-homepage/#stories"`, // child under its slug
		"/satelle-homepage/",                // child slug label
		"satelle workspace add",             // getting-started panel: the add command
		"satelle update",                    // getting-started panel: keep-current hint
	} {
		if !strings.Contains(body, want) {
			t.Errorf("projects page missing %q\n---\n%s", want, body)
		}
	}
	// The retired "launched here" badge must be gone — every project is uniform.
	if strings.Contains(body, "launched here") {
		t.Errorf("projects page still renders the retired 'launched here' badge:\n%s", body)
	}
	// Branded header parity (sty_4ea4d4df): the legacy "satelle." wordmark is gone
	// (the halfmoon brand-mark is the brand), the H1 matches the shared treatment,
	// and the body carries the live-refresh page marker.
	if strings.Contains(body, "satelle<span class=\"dot\">") {
		t.Errorf("landing still renders the legacy wordmark header:\n%s", body)
	}
	for _, want := range []string{"<h1>projects</h1>", `data-page="projects"`, "brand-mark"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing missing shared-header element %q", want)
		}
	}
	// A registered-but-failed child is an errored row, not silently omitted.
	for _, want := range []string{"not serving", "/repos/broken", "spawn: exec failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing missing errored-row element %q\n---\n%s", want, body)
		}
	}
}
