package verb_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func mkDep(t *testing.T, req map[string]any) (string, error) {
	t.Helper()
	req["title"] = "x"
	raw, err := dispatchRaw(t, "story-create", req)
	if err != nil {
		return "", err
	}
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	return it.ID, nil
}

func addTags(t *testing.T, id string, tags ...string) error {
	t.Helper()
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "add_tags": tags})
	return err
}

func mustContain(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a refusal containing %v, got none", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("refusal %q must contain %q", err, w)
		}
	}
}

func TestDependsOnAtCreateAndSet(t *testing.T) {
	wire(t)
	parent, _ := mkDep(t, map[string]any{"category": "epic-parent", "tags": []string{"epic:t"}})
	a, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	b, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	c, _ := mkDep(t, map[string]any{"tags": []string{"epic:t"}})
	outside, _ := mkDep(t, map[string]any{"tags": []string{"epic:other"}})

	// Repeated depends-on tags, each a story in the set, are accepted at create.
	if _, err := mkDep(t, map[string]any{"tags": []string{"epic:t", "depends-on:" + a, "depends-on:" + b}}); err != nil {
		t.Fatalf("repeated valid depends-on refused: %v", err)
	}

	// Not a story, and not an id at all.
	_, err := mkDep(t, map[string]any{"tags": []string{"epic:t", "depends-on:sty_nope"}})
	mustContain(t, err, "depends-on:sty_nope", "does not name a story")

	// A story outside the epic set: distinct message from not-a-story.
	_, err = mkDep(t, map[string]any{"tags": []string{"epic:t", "depends-on:" + outside}})
	mustContain(t, err, "depends-on:"+outside, "leaves the epic set")
	if strings.Contains(err.Error(), "does not name a story") {
		t.Errorf("an existing story outside the set must not read as not-a-story: %v", err)
	}

	// The parent need not carry the theme tag when it is parent_id.
	plain, _ := mkDep(t, map[string]any{"category": "fix"})
	if _, err := mkDep(t, map[string]any{"parent_id": plain, "tags": []string{"depends-on:" + plain}}); err != nil {
		t.Fatalf("depends-on the parent_id story refused: %v", err)
	}

	// No epic set at all.
	_, err = mkDep(t, map[string]any{"tags": []string{"depends-on:" + a}})
	mustContain(t, err, "depends-on:"+a, "no epic set")

	// Set: self-edge, 2-cycle, 3-cycle, and the accepted forward edges.
	mustContain(t, addTags(t, a, "depends-on:"+a), a+" depends on itself")
	if err := addTags(t, b, "depends-on:"+a); err != nil {
		t.Fatalf("b -> a: %v", err)
	}
	mustContain(t, addTags(t, a, "depends-on:"+b), "cycle", a+" -> "+b+" -> "+a)
	if err := addTags(t, c, "depends-on:"+b); err != nil {
		t.Fatalf("c -> b: %v", err)
	}
	mustContain(t, addTags(t, a, "depends-on:"+c), "cycle", a+" -> "+c+" -> "+b+" -> "+a)
	mustContain(t, addTags(t, a, "depends-on:"+outside), "depends-on:"+outside, "leaves the epic set")
	if err := addTags(t, a, "depends-on:"+parent); err != nil {
		t.Fatalf("a -> epic parent: %v", err)
	}
}
