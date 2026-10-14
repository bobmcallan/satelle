package cli

import "strings"

// joinNotice composes the user-visible session-start notice from the advisory
// lines that have something to say, one per line; "" when none do. The notice is
// the one channel agentcli.SessionContextOutput delivers to the user.
func joinNotice(lines ...string) string {
	var kept []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}
