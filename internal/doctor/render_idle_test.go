package doctor

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
)

// Every tier reads as its label followed by the file (or origin) that supplied
// the field, so the operator knows which file to edit (sty_5c56143b).
func TestRenderGrantSourcesNamesEveryTiersFile(t *testing.T) {
	for _, c := range []struct{ label, want string }{
		{"local", "← local (.satelle/workflows/agents.local.toml)"},
		{"repo", "← repo (.satelle/workflows/agents.toml)"},
		{"workspace", "← workspace (.satelle/workflows/agents.workspace.toml)"},
		{"profile:judge", "← profile:judge (~/.satelle/agents.toml)"},
		{"global-role:judge", "← global-role:judge (~/.satelle/agents.toml)"},
		{"baseline", "← baseline (embedded)"},
		{"embedded", "← embedded\n"},
	} {
		var b strings.Builder
		RenderGrantSources(&b, "", agentvalidate.Grant{Model: "m", Sources: map[string]string{"model": c.label}})
		if out := b.String(); !strings.Contains(out, `model = "m" `+c.want) {
			t.Errorf("tier %q: want %q in %q", c.label, c.want, out)
		}
	}
	var b strings.Builder
	RenderGrantSources(&b, "", agentvalidate.Grant{Sources: map[string]string{"env": "local+repo"}})
	if out := b.String(); !strings.Contains(out, "← local+repo (.satelle/workflows/agents.local.toml + .satelle/workflows/agents.toml)") ||
		!strings.Contains(out, "values withheld") {
		t.Errorf("a key-wise merged table names both files and withholds values: %q", out)
	}
}

func TestRenderGrantSourcesShowsIdleTimeout(t *testing.T) {
	var b strings.Builder
	RenderGrantSources(&b, "", agentvalidate.Grant{IdleTimeout: "5s", Sources: map[string]string{"idle_timeout": "repo"}})
	if out := b.String(); !strings.Contains(out, "idle_timeout") || !strings.Contains(out, "5s") || !strings.Contains(out, "repo") {
		t.Errorf("idle_timeout value and source missing: %q", out)
	}
}
