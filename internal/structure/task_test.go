package structure

import "testing"

// TestCheckTask covers the deterministic STRUCTURAL contract for a task
// work-definition file (sty_c1f9e74c): frontmatter id + kind: task + status, and
// a `# Title` heading. The richer work-definition contract is the gate's job.
func TestCheckTask(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		wantOK bool
	}{
		{"valid", "---\nid: tsk_1\nkind: task\nstatus: backlog\n---\n\n# Do a thing\n\nAudit x; verify x ran.", true},
		{"no frontmatter", "# Do a thing\n\nbody", false},
		{"missing id", "---\nkind: task\nstatus: backlog\n---\n\n# T\n\nb", false},
		{"wrong kind", "---\nid: tsk_1\nkind: story\nstatus: backlog\n---\n\n# T\n\nb", false},
		{"missing status", "---\nid: tsk_1\nkind: task\n---\n\n# T\n\nb", false},
		{"no title heading", "---\nid: tsk_1\nkind: task\nstatus: backlog\n---\n\njust prose, no heading", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := CheckTask(c.body)
			if c.wantOK && len(problems) != 0 {
				t.Errorf("want valid, got problems: %v", problems)
			}
			if !c.wantOK && len(problems) == 0 {
				t.Error("want problems, got none")
			}
		})
	}
}
