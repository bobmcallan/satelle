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
		ProjectsPage(w, r, projects)
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
}
