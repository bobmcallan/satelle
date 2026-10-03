package agentcli

import "strings"

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
	// payload did not carry one; it is passed on so a resume never widens it.
	Argv func(session, prompt, permissionMode string) []string
	// Basis is where the budget and the resume form were read from.
	Basis string
}

// stopResumes lists the harnesses with a recorded resume path. A harness absent
// here has none, which StopResumeFor reports by name.
var stopResumes = map[string]StopResume{
	HarnessGrok: {
		Cap:   8,
		Argv:  grokResumeArgv,
		Basis: "grok 1.0.46 binary's embedded docs: 'After 8 continuations (blocks or non-error feedback) in one turn the gate is overridden and the turn ends; hooks are not consulted for that final, forced stop. The counter is per turn: the next user prompt starts fresh', and headless 'grok -p \"…\" --resume \"<id>\"'",
	},
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

// StopResumeFor returns harness's Stop budget and resume path. ok is false for a
// harness with none — nothing unrecognised is assumed to be grok or claude.
func StopResumeFor(harness string) (StopResume, bool) {
	r, ok := stopResumes[harness]
	return r, ok
}

// ResumeEnv is environ for the resume command of harness: the same environment
// without the in-loop session markers that harness exports to its hook children,
// so the resumed process is not mistaken for a child of the session it resumes.
func ResumeEnv(harness string, environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, e := range environ {
		key, val, _ := strings.Cut(e, "=")
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
