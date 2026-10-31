package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dispatchPointersRules are the phrases each skill must carry together to apply
// satelle-dispatch-pointers (sty_73a2dc68 AC2): the plan skill makes the planner
// name what each dispatch's receiver reads for itself; the two reviews reject a
// plan or slice that embeds readable content in a dispatch, whatever its size.
var dispatchPointersRules = map[string][]string{
	"plan": {
		"[[satelle-dispatch-pointers]]",
		"For every dispatch the plan designs",
		"what it reads for itself",
		"never content it can fetch",
	},
	"satelle-story-plan-review": {
		"[[satelle-dispatch-pointers]]",
		"for every dispatch the plan designs",
		"what the receiving seat reads for itself",
		"embeds readable content in a dispatch",
		"is rejected whatever its size",
	},
	"satelle-code-ac-review": {
		"[[satelle-dispatch-pointers]]",
		"embeds readable content in a dispatch",
		"is rejected whatever its size",
		"a dispatch embeds readable content",
	},
}

// TestSkillsApplyDispatchPointers (sty_73a2dc68 AC2): the embedded plan gate and
// the two marketplace skills that ship from the tracked .satelle-market tree
// (plan, satelle-code-ac-review — not embedded, see substrate_ships_test.go)
// carry the dispatch-pointers rule, line wrapping ignored.
func TestSkillsApplyDispatchPointers(t *testing.T) {
	bodies := map[string]string{}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-story-plan-review" {
			bodies[d.Name] = d.Body
		}
	}
	for _, name := range []string{"plan", "satelle-code-ac-review"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", ".satelle-market", "skills", name+".md"))
		if err != nil {
			t.Fatalf("marketplace skill %s: %v", name, err)
		}
		bodies[name] = string(raw)
	}
	for name, phrases := range dispatchPointersRules {
		body, ok := bodies[name]
		if !ok || body == "" {
			t.Errorf("skill %s missing", name)
			continue
		}
		flat := strings.Join(strings.Fields(body), " ")
		for _, p := range phrases {
			if !strings.Contains(flat, p) {
				t.Errorf("%s does not apply satelle-dispatch-pointers: missing %q", name, p)
			}
		}
	}
}
