package workitem

import (
	"fmt"
	"strings"
	"time"
)

// Marshal renders a work item as portable markdown: a frontmatter block of the
// governed metadata, then the title, body, and acceptance criteria as markdown.
// Parse is its inverse, so a story round-trips losslessly between the store and a
// self-contained .md file that can be read, edited, and moved without the binary.
func Marshal(it Item) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", it.ID)
	fmt.Fprintf(&b, "kind: %s\n", it.Kind)
	fmt.Fprintf(&b, "status: %s\n", it.Status)
	if it.Priority != "" {
		fmt.Fprintf(&b, "priority: %s\n", it.Priority)
	}
	if it.Category != "" {
		fmt.Fprintf(&b, "category: %s\n", it.Category)
	}
	if it.ParentID != "" {
		fmt.Fprintf(&b, "parent: %s\n", it.ParentID)
	}
	if len(it.Tags) > 0 {
		fmt.Fprintf(&b, "tags: %s\n", strings.Join(it.Tags, ", "))
	}
	if !it.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created: %s\n", it.CreatedAt.UTC().Format(time.RFC3339))
	}
	if !it.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "updated: %s\n", it.UpdatedAt.UTC().Format(time.RFC3339))
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n", it.Title)
	if body := strings.TrimRight(it.Body, "\n"); body != "" {
		fmt.Fprintf(&b, "\n%s\n", body)
	}
	if ac := strings.TrimRight(it.AcceptanceCriteria, "\n"); ac != "" {
		fmt.Fprintf(&b, "\n## Acceptance Criteria\n\n%s\n", ac)
	}
	return []byte(b.String())
}

const acceptanceHeading = "## Acceptance Criteria"

// Parse reads a work-item markdown file back into an Item (the inverse of
// Marshal). The frontmatter carries the metadata; the body holds `# Title`, the
// description, and an `## Acceptance Criteria` section.
func Parse(data []byte) (Item, error) {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return Item{}, fmt.Errorf("workitem: markdown missing frontmatter")
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Item{}, fmt.Errorf("workitem: markdown frontmatter unterminated")
	}
	fm := rest[:end]
	body := strings.TrimLeft(rest[end+len("\n---"):], "\n")

	var it Item
	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			it.ID = v
		case "kind":
			it.Kind = Kind(v)
		case "status":
			it.Status = v
		case "priority":
			it.Priority = v
		case "category":
			it.Category = v
		case "parent":
			it.ParentID = v
		case "tags":
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					it.Tags = append(it.Tags, t)
				}
			}
		case "created":
			it.CreatedAt, _ = time.Parse(time.RFC3339, v)
		case "updated":
			it.UpdatedAt, _ = time.Parse(time.RFC3339, v)
		}
	}

	// Title is the first `# ` line; the remainder splits on the acceptance heading.
	after := body
	if strings.HasPrefix(body, "# ") {
		first, tail, _ := strings.Cut(body, "\n")
		it.Title = strings.TrimSpace(strings.TrimPrefix(first, "# "))
		after = tail
	}
	if idx := strings.Index(after, acceptanceHeading); idx >= 0 {
		it.Body = strings.TrimSpace(after[:idx])
		it.AcceptanceCriteria = strings.TrimSpace(after[idx+len(acceptanceHeading):])
	} else {
		it.Body = strings.TrimSpace(after)
	}
	return it, nil
}
