package agentcli

import (
	"os"
	"slices"
	"strconv"
	"strings"
)

// How a finished gate reaches a session whose turn cannot spend another Stop
// continuation (sty_eac9b28d, epic:gate-wake). A harness that caps in-turn
// continuations records its cap and how to resume the same session with the
// verdict as a fresh prompt; the engine and the hook read only this record and
// never name a harness.

// StopResume is one harness's Stop-continuation budget and its resume path.
type StopResume struct {
	// Cap is how many Stop emissions a turn may spend (a block, or any non-error
	// Stop feedback). The one after the last is not delivered: the turn ends.
	Cap int
	// Argv is the command that resumes session with prompt as the new turn's
	// prompt. permissionMode is the mode the session ran under, "" when the Stop
	// payload did not carry one; it is passed on so a resume never widens it. Nil
	// for a harness with a recorded budget and no resume path: StopCapFor answers
	// for it, StopResumeFor does not.
	Argv func(session, prompt, permissionMode string) []string
	// Basis is where the budget and the resume form were read from.
	Basis string
	// CapEnv, when set, names the environment variable that overrides Cap for
	// the session the hook runs in. Hook and resume process inherit it, so the
	// budget the hook counts against is the one the harness enforces.
	CapEnv string
	// KeepEnv names variables ResumeEnv keeps although they match a session
	// marker: the resumed process is the same user's continuation of the same
	// session, so its budget override and its credentials must carry over.
	KeepEnv []string
}

// stopResumes lists the harnesses with a recorded Stop budget, and the resume
// path where one exists. A harness absent here has neither, which StopResumeFor
// reports by name.
var stopResumes = map[string]StopResume{
	// cursor counts a followup_message as a continuation of the same turn. With
	// loop_limit unset, stop fired at loop_count 0 to 4 and the followup emitted at
	// 4 produced no further stop. cursor-agent -p dispatches no stop event, so
	// there is no headless resume path to record: no Argv.
	HarnessCursor: {
		Cap:   4,
		Basis: "cursor-agent 2026.10.01 probe 20-cap (testdata/cursor/20-cap-hooks.log): loop_limit unset, stop fired at loop_count 0-4; a followup at 0-3 produced another turn, the followup at 4 produced none",
	},
	HarnessGrok: {
		Cap:   8,
		Argv:  grokResumeArgv,
		Basis: "grok 1.0.46 binary's embedded docs: 'After 8 continuations (blocks or non-error feedback) in one turn the gate is overridden and the turn ends; hooks are not consulted for that final, forced stop. The counter is per turn: the next user prompt starts fresh', and headless 'grok -p \"…\" --resume \"<id>\"'",
	},
	HarnessClaude: {
		Cap:     8,
		CapEnv:  "CLAUDE_CODE_STOP_HOOK_BLOCK_CAP",
		KeepEnv: []string{"CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "CLAUDE_CODE_OAUTH_TOKEN"},
		Argv:    claudeResumeArgv,
		Basis:   "claude 2.1.288: CLAUDE_CODE_STOP_HOOK_BLOCK_CAP defaults to 8 and a live always-block Stop probe was overridden after 9 consecutive blocks (session db658341-256f-413c-ac4e-c6c48922fbf5, 2026-10-03); headless 'claude -p \"…\" --resume <session-id>' continues that session",
	},
}

// claudeResumeArgv is claude's headless resume: -p carries the prompt and
// --resume names the session, so the verdict is the next user prompt of the same
// session. --permission-mode carries the session's own mode over.
func claudeResumeArgv(session, prompt, permissionMode string) []string {
	argv := []string{"claude", "-p", prompt, "--resume", session}
	if permissionMode != "" {
		argv = append(argv, "--permission-mode", permissionMode)
	}
	return argv
}

// grokResumeArgv is grok's headless resume: -p carries the prompt and --resume
// names the session, so the verdict is the next user prompt of the same session,
// not a second session. --permission-mode (a documented global flag) carries the
// session's own mode over so the resumed turn can do what the session could.
func grokResumeArgv(session, prompt, permissionMode string) []string {
	argv := []string{"grok", "-p", prompt, "--resume", session}
	if permissionMode != "" {
		argv = append(argv, "--permission-mode", permissionMode)
	}
	return argv
}

// stopBudget is harness's recorded entry with its environment override applied.
func stopBudget(harness string) (StopResume, bool) {
	r, ok := stopResumes[harness]
	if ok && r.CapEnv != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(r.CapEnv))); err == nil && n > 0 {
			r.Cap = n
		}
	}
	return r, ok
}

// StopResumeFor returns harness's Stop budget and resume path. ok is false for a
// harness with no resume path — one absent from the table, or one whose entry
// records a budget but no Argv — and nothing unrecognised is assumed to be grok
// or claude. A harness that lets the environment override its cap gets the
// overridden Cap; an unset or non-positive value leaves the recorded default.
func StopResumeFor(harness string) (StopResume, bool) {
	r, ok := stopBudget(harness)
	if !ok || r.Argv == nil {
		return StopResume{}, false
	}
	return r, true
}

// StopCapFor returns how many Stop continuations a turn of harness may spend,
// with or without a resume path. ok is false for a harness with no recorded
// budget. The environment override applies as it does for StopResumeFor.
func StopCapFor(harness string) (int, bool) {
	r, ok := stopBudget(harness)
	if !ok {
		return 0, false
	}
	return r.Cap, true
}

// ResumeEnv is environ for the resume command of harness: the same environment
// without the in-loop session markers that harness exports to its hook children,
// so the resumed process is not mistaken for a child of the session it resumes.
func ResumeEnv(harness string, environ []string) []string {
	keep := stopResumes[harness].KeepEnv
	out := make([]string, 0, len(environ))
	for _, e := range environ {
		key, val, _ := strings.Cut(e, "=")
		if slices.Contains(keep, key) {
			out = append(out, e)
			continue
		}
		marked := false
		for _, m := range sessionMarkers {
			if m.harness != harness {
				continue
			}
			if (m.prefix && strings.HasPrefix(key, m.key)) || (!m.prefix && key == m.key) {
				marked = marked || m.match(val)
			}
		}
		if !marked {
			out = append(out, e)
		}
	}
	return out
}

// StopResumeUnavailable is the adapter-named reason a harness has no resume
// path, for the places that must say so rather than stay silent.
func StopResumeUnavailable(harness string) string {
	if harness == "" {
		harness = HarnessUnknown
	}
	return "unavailable: " + harness + ": no Stop-continuation budget or session-resume path is recorded for this harness"
}
