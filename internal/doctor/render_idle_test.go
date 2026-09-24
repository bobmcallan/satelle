package doctor

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
)

func TestRenderGrantSourcesShowsIdleTimeout(t *testing.T) {
	var b strings.Builder
	RenderGrantSources(&b, "", agentvalidate.Grant{IdleTimeout: "5s", Sources: map[string]string{"idle_timeout": "repo"}})
	if out := b.String(); !strings.Contains(out, "idle_timeout") || !strings.Contains(out, "5s") || !strings.Contains(out, "repo") {
		t.Errorf("idle_timeout value and source missing: %q", out)
	}
}
