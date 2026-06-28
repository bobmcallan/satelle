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

func TestAssignSlugsDeduplicates(t *testing.T) {
	got := AssignSlugs([]Project{
		{Name: "repo"}, {Name: "Repo"}, {Name: "repo"}, {Name: "other"},
	})
	want := []string{"repo", "repo-2", "repo-3", "other"}
	for i, w := range want {
		if got[i].Slug != w {
			t.Errorf("project %d slug = %q, want %q", i, got[i].Slug, w)
		}
	}
}

func TestMultiHomeListsProjectsWithPerPortLinks(t *testing.T) {
	projects := AssignSlugs([]Project{
		{Name: "satelle", Path: "/repos/satelle", Port: 8801},
		{Name: "satelle-homepage", Path: "/repos/satelle-homepage", Port: 8802},
	})
	srv := httptest.NewServer(NewMultiHandler(func() []Project { return projects }))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Host = "localhost:8787"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	for _, want := range []string{
		"satelle</div>", "/satelle</span>",
		"satelle-homepage</div>", "/satelle-homepage</span>",
		`href="http://localhost:8801/#stories"`,
		`href="http://localhost:8802/#stories"`,
		"projects served",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("homepage missing %q\n---\n%s", want, body)
		}
	}
}

func TestMultiHandlerHealthz(t *testing.T) {
	srv := httptest.NewServer(NewMultiHandler(func() []Project { return nil }))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("healthz = %d", resp.StatusCode)
	}
}
