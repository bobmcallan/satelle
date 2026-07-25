package structure

import "strings"

// CheckFence extracts the contents of the first fenced code block whose info
// string is `check` (```check or ```check sh) — the self-contained functional
// check embedded in a skill body. Returns "" when none.
//
// Single source for the engine runner, structure presence checks, and test
// golden tables (sty_6830e78e). Pure extract; no semantic change from the
// prior bodyCheckBlock / hasCheckBlock copies.
func CheckFence(body string) string {
	lines := strings.Split(body, "\n")
	in := false
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(t, "```") {
				info := strings.TrimSpace(strings.TrimPrefix(t, "```"))
				if info == "check" || strings.HasPrefix(info, "check ") {
					in = true
				}
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			break // closing fence
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
