package agentcli

import "slices"

// Per-adapter tool vocabularies (sty_259572f6). A seat's `tools` grant is written
// in its adapter's own tool names, and the names do not travel: pi's `read` is no
// Claude tool, Claude's `Read` no pi one. When a profile change moves a seat to
// another adapter, a layer that restated the grant in the old names leaves the
// seat with tools its harness does not offer. ForeignToolNames names that drift.

// claudeBuiltinTools is the Claude CLI's built-in tool set, as its --tools and
// --allowedTools flags name them.
var claudeBuiltinTools = []string{
	"Read", "Write", "Edit", "Bash", "Grep", "Glob", "LS",
	"WebFetch", "WebSearch", "NotebookEdit", "Task", "TodoWrite",
}

// toolVocabularies are the adapters whose tool names satelle knows, in the order
// a foreign name is attributed. An adapter absent here is judged by nothing.
var toolVocabularies = []struct {
	adapter string
	tools   []string
}{
	{HarnessClaude, claudeBuiltinTools},
	{HarnessPi, piBuiltinTools},
}

// ForeignToolNames returns the tools in grant written in another known adapter's
// names rather than adapter's own — pi's `read` under Claude, Claude's `Read`
// under pi — and the adapter those names belong to. Names match exactly, case
// included, as each harness reads them. A wildcard, a scoped pattern such as
// `Bash(satelle:*)`, and a name in neither vocabulary are not judged. It returns
// nothing for an adapter satelle has no vocabulary for, so no harness is assumed
// ([[satelle-agent-agnostic]]).
func ForeignToolNames(adapter, grant string) (owner string, names []string) {
	own, known := vocabularyOf(adapter)
	if !known {
		return "", nil
	}
	for _, t := range parseGrant(grant) {
		if t.Scoped || t.Name == "*" || slices.Contains(own, t.Name) {
			continue
		}
		for _, v := range toolVocabularies {
			if v.adapter == adapter || !slices.Contains(v.tools, t.Name) {
				continue
			}
			if owner == "" {
				owner = v.adapter
			}
			if v.adapter == owner {
				names = append(names, t.Name)
			}
			break
		}
	}
	return owner, names
}

func vocabularyOf(adapter string) ([]string, bool) {
	for _, v := range toolVocabularies {
		if v.adapter == adapter {
			return v.tools, true
		}
	}
	return nil, false
}
