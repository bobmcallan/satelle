package verb

import (
	"strings"
	"testing"
)

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestValidateDependsOn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		self    string
		targets []string
		exists  map[string]bool
		allowed map[string]bool
		scope   string
		edges   map[string][]string
		want    []string // substrings of the refusal; nil = accepted
	}{
		{name: "two repeated targets in the set", self: "sty_a", targets: []string{"sty_b", "sty_c"},
			exists: set("sty_a", "sty_b", "sty_c"), allowed: set("sty_a", "sty_b", "sty_c"), scope: "epic:x"},
		{name: "not a story", self: "sty_a", targets: []string{"tsk_9"},
			exists: set("sty_a"), allowed: set("sty_a"), scope: "epic:x",
			want: []string{"depends-on:tsk_9", "does not name a story"}},
		{name: "outside the epic set", self: "sty_a", targets: []string{"sty_z"},
			exists: set("sty_a", "sty_z"), allowed: set("sty_a"), scope: "epic:x",
			want: []string{"depends-on:sty_z", "leaves the epic set", "sty_a", "epic:x"}},
		{name: "no epic set at all", self: "", targets: []string{"sty_z"},
			exists: set("sty_z"), allowed: set(),
			want: []string{"depends-on:sty_z", "no epic set"}},
		{name: "self edge", self: "sty_a", targets: []string{"sty_a"},
			exists: set("sty_a"), allowed: set("sty_a"), scope: "epic:x",
			want: []string{"sty_a depends on itself"}},
		{name: "two cycle", self: "sty_a", targets: []string{"sty_b"},
			exists: set("sty_a", "sty_b"), allowed: set("sty_a", "sty_b"), scope: "epic:x",
			edges: map[string][]string{"sty_b": {"sty_a"}},
			want:  []string{"cycle", "sty_a -> sty_b -> sty_a"}},
		{name: "three cycle", self: "sty_a", targets: []string{"sty_b"},
			exists: set("sty_a", "sty_b", "sty_c"), allowed: set("sty_a", "sty_b", "sty_c"), scope: "epic:x",
			edges: map[string][]string{"sty_b": {"sty_c"}, "sty_c": {"sty_a"}},
			want:  []string{"cycle", "sty_a -> sty_b -> sty_c -> sty_a"}},
		{name: "diamond is not a cycle", self: "sty_a", targets: []string{"sty_b", "sty_c"},
			exists: set("sty_a", "sty_b", "sty_c", "sty_d"), allowed: set("sty_a", "sty_b", "sty_c", "sty_d"), scope: "epic:x",
			edges: map[string][]string{"sty_b": {"sty_d"}, "sty_c": {"sty_d"}}},
		{name: "new story cannot cycle", self: "", targets: []string{"sty_b"},
			exists: set("sty_b"), allowed: set("sty_b"), scope: "epic:x",
			edges: map[string][]string{"sty_b": {"sty_b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDependsOn(tc.self, tc.targets, tc.exists, tc.allowed, tc.scope, tc.edges)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want refusal containing %v, got none", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q must contain %q", err, w)
				}
			}
		})
	}
}

func TestDependsOnTargetsDeduplicatesAndIgnoresOtherTags(t *testing.T) {
	got := dependsOnTargets([]string{"order:1", "depends-on:sty_a", "blocked-by:sty_x", "depends-on:sty_b", "depends-on:sty_a"})
	if strings.Join(got, ",") != "sty_a,sty_b" {
		t.Errorf("targets = %v, want [sty_a sty_b]", got)
	}
}
