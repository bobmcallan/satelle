package help

import (
	"regexp"
	"testing"
)

// satelle manages no cloud identity (sty_c00782a3): no help topic may claim it
// signs in, selects or isolates one. The retirement sentence ("no longer manages
// cloud identity") is the one sanctioned mention and does not match.
func TestNoTopicClaimsCloudIdentityHandling(t *testing.T) {
	claim := regexp.MustCompile(`(?i)signs? (the session )?in as|(select|isolat)\w* (a |the )?cloud identity|cloud identity a launcher|az login|credential_process`)
	for _, top := range List() {
		if loc := claim.FindStringIndex(top.Body); loc != nil {
			t.Errorf("topic %q claims cloud identity handling: %q", top.Name, top.Body[loc[0]:loc[1]])
		}
	}
	for _, name := range []string{"global-agents", "agent-dispatch"} {
		top, ok := Get(name)
		if !ok {
			t.Fatalf("missing topic %q", name)
		}
		if name == "global-agents" && !regexp.MustCompile(`no longer manages cloud identity`).MatchString(top.Body) {
			t.Errorf("%s does not say satelle no longer manages cloud identity", name)
		}
	}
}
