package agentcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// Cloud launch (sty_82cffd60, epic:hosted-workspace): starting a session of the
// repository on a provider's cloud from a local worktree, and naming the branch
// that session pushes its work to. Which harnesses can, how, and what the branch
// is called is provider knowledge, so it lives here and nowhere else: a harness
// with no launcher registered is explicitly unavailable
// (satelle-agent-agnostic §2), never defaulted to Claude.

// CloudSession is what a launcher learned about the session it started.
type CloudSession struct {
	ID  string
	URL string
	// Title is the provider's name for the session, when it prints one.
	Title string
}

// CloudLauncher starts a cloud session of the repository checked out at dir
// (its pushed branch is what the session is based on), seeded with prompt.
type CloudLauncher func(ctx context.Context, dir, prompt string) (CloudSession, error)

// ErrCloudUnavailable is the explicit "this harness has no cloud runner".
type ErrCloudUnavailable struct {
	Harness string
	// Reason, when set, says why a harness that has a runner cannot use it here.
	Reason string
}

func (e ErrCloudUnavailable) Error() string {
	msg := "cloud session: unavailable for adapter " + e.Harness
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg
}

// cloudBranchers names the branch a harness's cloud session pushes to, keyed by
// harness token, from the story id and a per-dispatch nonce. The shape is the
// provider's (what its cloud lets a session push); the engine never builds one.
var cloudBranchers = map[string]func(storyID, nonce string) string{
	HarnessClaude: func(storyID, nonce string) string { return "claude/satelle-" + storyID + "-" + nonce },
}

// CloudBranch is the branch harness's cloud session is told to push, or
// ErrCloudUnavailable for a harness with no cloud runner.
func CloudBranch(harness, storyID, nonce string) (string, error) {
	name, ok := cloudBranchers[harness]
	if !ok {
		return "", ErrCloudUnavailable{Harness: harness}
	}
	return name(storyID, nonce), nil
}

// cloudLaunchers is the registry, keyed by harness token. Only a harness that
// has a runner is listed.
var cloudLaunchers = map[string]CloudLauncher{
	HarnessClaude: launchClaudeCloud,
}

// cloudHostReason names why a harness that has a launcher cannot use it on this
// host; empty when it can.
var cloudHostReason = func(harness string) string {
	if harness == HarnessClaude && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "claude --cloud needs a pty, which satelle provides through script on linux and darwin only"
	}
	return ""
}

// CloudLaunchAvailable is nil when harness has a launcher that runs on this
// host, else the ErrCloudUnavailable a launch would return. It attempts nothing,
// so a caller can refuse before it does any work.
func CloudLaunchAvailable(harness string) error {
	if _, ok := cloudLaunchers[harness]; !ok {
		return ErrCloudUnavailable{Harness: harness}
	}
	if r := cloudHostReason(harness); r != "" {
		return ErrCloudUnavailable{Harness: harness, Reason: r}
	}
	return nil
}

// LaunchCloud starts a cloud session through harness's launcher. A harness with
// none, or one that cannot run on this host, returns ErrCloudUnavailable
// without attempting anything.
func LaunchCloud(ctx context.Context, harness, dir, prompt string) (CloudSession, error) {
	if err := CloudLaunchAvailable(harness); err != nil {
		return CloudSession{}, err
	}
	return cloudLaunchers[harness](ctx, dir, prompt)
}

// SetCloudLauncher registers fn for harness and returns a function that restores
// the previous registration. It is the test seam: nothing in production calls it.
// A harness registered this way with no branch namer gets the
// "<harness>/satelle-<story>-<nonce>" shape, so a fake harness is usable whole.
func SetCloudLauncher(harness string, fn CloudLauncher) (restore func()) {
	prev, had := cloudLaunchers[harness]
	prevName, hadName := cloudBranchers[harness]
	cloudLaunchers[harness] = fn
	if !hadName {
		cloudBranchers[harness] = func(storyID, nonce string) string { return harness + "/satelle-" + storyID + "-" + nonce }
	}
	return func() {
		if had {
			cloudLaunchers[harness] = prev
		} else {
			delete(cloudLaunchers, harness)
		}
		if hadName {
			cloudBranchers[harness] = prevName
		} else {
			delete(cloudBranchers, harness)
		}
	}
}

// cloudLaunchCapability is the CapabilityTable cloud-launch cell for an adapter
// row ("<provider> <transport>"): available when its provider has a launcher
// registered, else unavailable with the adapter-named reason. A host that cannot
// run a registered launcher is reported by LaunchCloud when asked to launch.
func cloudLaunchCapability(adapter string) Capability {
	harness, _, _ := strings.Cut(adapter, " ")
	if _, ok := cloudLaunchers[harness]; !ok {
		return no(adapter + " has no cloud runner")
	}
	return yes()
}

// launchClaudeCloud runs `claude --cloud "<prompt>"` from dir. The CLI refuses
// without an interactive terminal, so it runs under a pty provided by script(1);
// the prompt travels in the environment, never through shell quoting. The
// command prints the session and returns at once — the session runs in the
// cloud — so its output is parsed rather than streamed.
func launchClaudeCloud(ctx context.Context, dir, prompt string) (CloudSession, error) {
	const promptEnv = "SATELLE_CLOUD_PROMPT"
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(ctx, "script", "-qfc", CLIClaude+` --cloud "$`+promptEnv+`"`, "/dev/null")
	default: // darwin; LaunchCloud has refused every other host
		cmd = exec.CommandContext(ctx, "script", "-q", "/dev/null", CLIClaude, "--cloud", prompt)
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), promptEnv+"="+prompt)
	out, err := cmd.CombinedOutput()
	if s, perr := ParseClaudeCloudOutput(string(out)); perr == nil {
		return s, nil
	} else if err == nil {
		return CloudSession{}, perr
	}
	return CloudSession{}, fmt.Errorf("cloud session: claude --cloud failed: %w: %s", err, strings.TrimSpace(StripTerminalCodes(string(out))))
}

// claudeCloudURL is the session address, whose last path segment is the id.
var claudeCloudURL = regexp.MustCompile(`https://claude\.ai/code/(session_[A-Za-z0-9]+)`)

const claudeCloudCreated = "Created cloud session:"

// StripTerminalCodes removes ANSI escape sequences and carriage returns, which a
// pty adds to what the program wrote.
func StripTerminalCodes(s string) string {
	return strings.ReplaceAll(ansiPattern.ReplaceAllString(s, ""), "\r", "")
}

// ParseClaudeCloudOutput reads what `claude --cloud` prints on success:
//
//	Created cloud session: <title>
//	View: https://claude.ai/code/session_<id>
//	Resume with: claude --teleport session_<id>
//
// It fails, naming what is missing, rather than inventing a session.
func ParseClaudeCloudOutput(out string) (CloudSession, error) {
	clean := StripTerminalCodes(out)
	_, rest, ok := strings.Cut(clean, claudeCloudCreated)
	if !ok {
		return CloudSession{}, fmt.Errorf("cloud session: claude --cloud printed no %q line: %s", claudeCloudCreated, strings.TrimSpace(clean))
	}
	m := claudeCloudURL.FindStringSubmatch(rest)
	if m == nil {
		return CloudSession{}, fmt.Errorf("cloud session: claude --cloud printed no session URL after %q: %s", claudeCloudCreated, strings.TrimSpace(clean))
	}
	title, _, _ := strings.Cut(rest, "\n")
	return CloudSession{ID: m[1], URL: m[0], Title: strings.TrimSpace(title)}, nil
}
