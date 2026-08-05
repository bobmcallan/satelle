// harness_hooks.go — the ONE definition of each harness's satelle hook surface
// (sty_338a53f8). Three paths used to answer "what does a satelle scaffold look
// like for this harness" independently: the BUILDER that writes a fresh file,
// the HEALER that appends missing entries to an existing one, and the CHECKER
// that decides whether the result is complete. They disagreed — the healer
// expected Codex PreToolUse matchers the builder never writes, and the checker
// demanded a Stop hook the Codex builder deliberately omits, so every re-init
// WARNed about a file that was exactly right. All three now read this table.
package cli

// harnessHookSpec is one harness's satelle hook surface.
//
// gateMatcher / commitMatcher are the PreToolUse matchers: the tool-name
// alternation the scaffold writes, and therefore the only one the healer may
// expect. events is the set of hook events a satelle scaffold for this harness
// actually installs — and therefore the set the completeness check may demand.
type harnessHookSpec struct {
	gateMatcher   string
	commitMatcher string
	events        []string
}

// hasEvent reports whether this harness's scaffold installs the named event.
func (s harnessHookSpec) hasEvent(event string) bool {
	for _, e := range s.events {
		if e == event {
			return true
		}
	}
	return false
}

// fullHookEvents is the event set a harness gets when it supports all of them.
var fullHookEvents = []string{"SessionStart", "PreToolUse", "UserPromptSubmit", "Stop"}

// harnessHooks returns the hook surface for a harness. An unknown harness gets
// the claude shape, which is what every caller already defaulted to.
//
// Codex is the harness that differs, in both fields:
//
//   - MATCHERS carry only the tool names Codex documents — apply_patch (with its
//     Edit|Write aliases) and Bash. Speculative names (write_file, shell) were
//     expected by the old heal path and written by nothing, so they are absent
//     here too: a matcher the builder never emits cannot be a heal target, and
//     inventing tool names for a harness is exactly what a scaffold must not do.
//     Narrowing rather than widening is deliberate — do not re-add them without
//     evidence from Codex that the tool exists.
//   - EVENTS omit Stop. The Codex scaffold uses only the events proven in the
//     sty_9e86f407 plan probe; stopcheck runs on Claude and Grok. A checker that
//     demands Stop of Codex contradicts the builder that omits it.
func harnessHooks(harness string) harnessHookSpec {
	switch harness {
	case "grok":
		return harnessHookSpec{
			gateMatcher:   "Edit|Write|MultiEdit|NotebookEdit|search_replace|write",
			commitMatcher: "Bash|run_terminal_command",
			events:        fullHookEvents,
		}
	case "codex":
		return harnessHookSpec{
			gateMatcher:   "apply_patch|Edit|Write|Bash",
			commitMatcher: "Bash",
			events:        []string{"SessionStart", "PreToolUse", "UserPromptSubmit"},
		}
	default: // claude
		return harnessHookSpec{
			gateMatcher:   "Edit|Write|MultiEdit|NotebookEdit",
			commitMatcher: "Bash",
			events:        fullHookEvents,
		}
	}
}
