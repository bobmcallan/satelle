package verb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DependsOnPrefix prefixes the tag that declares one story's dependency on
// another story in the same epic set. Distinct from `order:` (sprint position)
// and `blocked-by:` (the park cue for a world that is not ready). The binary
// validates the declaration; it does not compute a wave or change engagement
// from it.
const DependsOnPrefix = "depends-on:"

// dependsOnTargets returns the story ids tags declare a dependency on, in tag
// order, each once.
func dependsOnTargets(tags []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tags {
		if !strings.HasPrefix(t, DependsOnPrefix) {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(t, DependsOnPrefix))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// validateDependsOn is the pure core: it judges one story's declared targets
// against facts the caller loaded. self is "" for a story not yet created.
// exists says which targets are stories; allowed is the story's epic set (scope
// names it for the message, "" when the story has none); edges are the
// depends-on edges already stored for the other members. The checks run in a
// fixed order so each refusal names exactly one defect and the ids involved.
func validateDependsOn(self string, targets []string, exists, allowed map[string]bool, scope string, edges map[string][]string) error {
	for _, t := range targets {
		if !exists[t] {
			return fmt.Errorf("%s%s does not name a story", DependsOnPrefix, t)
		}
	}
	for _, t := range targets {
		if self != "" && t == self {
			return fmt.Errorf("%s depends on itself (%s%s)", self, DependsOnPrefix, t)
		}
	}
	for _, t := range targets {
		if allowed[t] {
			continue
		}
		if scope == "" {
			return fmt.Errorf("%s%s has no epic set to stay inside: the story carries no %s<theme> tag and no parent", DependsOnPrefix, t, epicset.TagPrefix)
		}
		return fmt.Errorf("%s%s leaves the epic set of %s (%s)", DependsOnPrefix, t, selfLabel(self), scope)
	}
	if self == "" {
		// A story not yet created has no incoming edge, so it cannot close a cycle.
		return nil
	}
	graph := make(map[string][]string, len(edges)+1)
	for id, to := range edges {
		graph[id] = to
	}
	graph[self] = targets
	if path := dependsOnCycle(self, graph); path != nil {
		return fmt.Errorf("depends-on cycle: %s", strings.Join(path, " -> "))
	}
	return nil
}

func selfLabel(self string) string {
	if self == "" {
		return "the new story"
	}
	return self
}

// dependsOnCycle returns the path self -> ... -> self when graph holds a cycle
// through self, else nil.
func dependsOnCycle(self string, graph map[string][]string) []string {
	var path []string
	visited := map[string]bool{self: true}
	var walk func(n string) bool
	walk = func(n string) bool {
		path = append(path, n)
		for _, next := range graph[n] {
			if next == self {
				path = append(path, self)
				return true
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			if walk(next) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	if walk(self) {
		return path
	}
	return nil
}

// dependsOnStore is the slice of the work-item store the check reads.
type dependsOnStore interface {
	epicset.Lister
	Get(ctx context.Context, id string) (workitem.Item, error)
}

// checkDependsOn loads what validateDependsOn needs and runs it. tags is the
// story's FINAL effective tag set; self is "" on create. It is a no-op when the
// tags declare no dependency, so every other flow is unchanged.
func checkDependsOn(ctx context.Context, store dependsOnStore, self, parentID string, tags []string) error {
	targets := dependsOnTargets(tags)
	if len(targets) == 0 {
		return nil
	}
	exists := make(map[string]bool, len(targets))
	for _, t := range targets {
		it, err := store.Get(ctx, t)
		switch {
		case errors.Is(err, workitem.ErrNotFound):
		case err != nil:
			return fmt.Errorf("verb: read %s%s: %w", DependsOnPrefix, t, err)
		default:
			exists[t] = it.Kind == workitem.KindStory
		}
	}

	// The epic set: every story carrying one of this story's epic:<theme> tags
	// (the parent carries the same tag), plus the parent_id link, which may not.
	allowed := map[string]bool{}
	edges := map[string][]string{}
	var scope []string
	for _, theme := range epicset.Themes(workitem.Item{Tags: tags}) {
		scope = append(scope, theme)
		items, err := store.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Tag: theme, Limit: 2000})
		if err != nil {
			return fmt.Errorf("verb: list %s: %w", theme, err)
		}
		for _, it := range items {
			allowed[it.ID] = true
			if to := dependsOnTargets(it.Tags); len(to) > 0 {
				edges[it.ID] = to
			}
		}
	}
	if parentID != "" {
		allowed[parentID] = true
		scope = append(scope, "parent "+parentID)
	}
	sort.Strings(scope)
	return validateDependsOn(self, targets, exists, allowed, strings.Join(scope, ", "), edges)
}
