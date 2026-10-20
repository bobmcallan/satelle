package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sty_12499ecc AC3: a story carries references to its source requirements in a
// tag namespace the repo declares, and `story get` and `story route` show them as
// references. A tag in a namespace the repo did not declare stays a plain tag.
// The namespace "ref" and the system "jira" are configuration in this fixture;
// the binary knows neither.

func referencesRepo(t *testing.T) {
	t.Helper()
	repo := tempRepo(t)
	if out, err := runRoot(t, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "[tags]") {
		t.Fatalf("fixture assumes no [tags] table yet:\n%s", body)
	}
	if err := os.WriteFile(cfgPath, append(body, []byte("\n[tags]\nreferences = [\"ref\"]\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createTagged(t *testing.T, tags string) string {
	t.Helper()
	out, err := runRoot(t, "story", "create", "--title", "t", "--category", "feature", "--tags", tags)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &it); err != nil || it.ID == "" {
		t.Fatalf("create output is not a story: %v\n%s", err, out)
	}
	return it.ID
}

func TestStoryGetAndRouteShowDeclaredReferences(t *testing.T) {
	referencesRepo(t)
	id := createTagged(t, "ref:jira:ID-12,surface:cli")

	out, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, out)
	}
	var got struct {
		Tags       []string `json:"tags"`
		References []struct {
			System string `json:"system"`
			Key    string `json:"key"`
			Tag    string `json:"tag"`
		} `json:"references"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("get is not JSON: %v\n%s", err, out)
	}
	if len(got.References) != 1 || got.References[0].System != "jira" || got.References[0].Key != "ID-12" || got.References[0].Tag != "ref:jira:ID-12" {
		t.Errorf("references = %+v, want the one jira ID-12 reference", got.References)
	}
	if !contains(got.Tags, "ref:jira:ID-12") || !contains(got.Tags, "surface:cli") {
		t.Errorf("tags = %v, the reference stays a tag too", got.Tags)
	}

	route, err := runRoot(t, "story", "route", id)
	if err != nil {
		t.Fatalf("route: %v\n%s", err, route)
	}
	if !strings.Contains(route, "## References") || !strings.Contains(route, "jira ID-12") || !strings.Contains(route, "`ref:jira:ID-12`") {
		t.Errorf("route does not show the reference:\n%s", route)
	}
	if strings.Contains(route, "surface cli") {
		t.Errorf("a plain tag was shown as a reference:\n%s", route)
	}
}

func TestStoryGetAndRouteShowAnUndeclaredNamespaceAsAPlainTag(t *testing.T) {
	referencesRepo(t)
	id := createTagged(t, "other:jira:ID-9,ref:malformed")

	out, err := runRoot(t, "story", "get", id)
	if err != nil {
		t.Fatalf("get: %v\n%s", err, out)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("get is not JSON: %v\n%s", err, out)
	}
	if _, has := got["references"]; has {
		t.Errorf("an undeclared namespace (and a malformed reference) became a reference: %s", got["references"])
	}
	var tags []string
	if err := json.Unmarshal(got["tags"], &tags); err != nil || !contains(tags, "other:jira:ID-9") || !contains(tags, "ref:malformed") {
		t.Errorf("tags = %v (%v), want both as plain tags", tags, err)
	}
	route, err := runRoot(t, "story", "route", id)
	if err != nil {
		t.Fatalf("route: %v\n%s", err, route)
	}
	if strings.Contains(route, "## References") {
		t.Errorf("route shows references for plain tags:\n%s", route)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
