// Repo-local satelle binary (sty_fe3ee313): a repo may pin its own satelle under
// .satelle/satelle (installed by `satelle update --local`). When that pin is
// present, it is the binary that runs for the repo — the globally-invoked satelle
// re-execs the local one at startup. This keeps the product repo-agnostic
// (satelle-repo-agnostic): the pin is an installed RELEASE asset, never a source
// build, and nothing about any one repo is baked into the binary.
package cli

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/config"
)

// localExecMarker is set in the re-exec'd child's environment so the local binary
// does not itself re-exec — the loop guard.
const localExecMarker = "SATELLE_LOCAL_EXEC"

// localBinaryName is the repo-local pin under the data dir (.satelle/satelle).
const localBinaryName = "satelle"

// findDotSatelleRoot walks up from start looking for a directory that contains a
// .satelle/ subdir, returning that directory. The repo root is where .satelle/
// lives, mirroring how the rest of satelle resolves a repo.
func findDotSatelleRoot(start string) (string, bool) {
	dir := start
	for {
		if fi, err := os.Stat(filepath.Join(dir, config.DefaultDataDir)); err == nil && fi.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// repoLocalTarget is the path `satelle update --local` installs to: the nearest
// ancestor's <repo>/.satelle/satelle, or <cwd>/.satelle/satelle when no .satelle/
// exists yet (a not-yet-initialised repo). replaceExecutable creates the dir.
func repoLocalTarget() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	root := cwd
	if r, ok := findDotSatelleRoot(cwd); ok {
		root = r
	}
	return filepath.Join(root, config.DefaultDataDir, localBinaryName)
}

// resolvePathOrSelf evaluates symlinks for p, falling back to p unchanged when it
// cannot (e.g. p does not exist). Used to compare two binary paths by identity.
func resolvePathOrSelf(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// localReexecTarget is the PURE decision behind the local-binary dispatch: given
// the cwd, the currently-running executable path (self), and whether the loop
// marker is already set, it returns the repo-local binary to re-exec and true —
// or ("", false) to run in-process. It re-execs when a .satelle/satelle pin exists
// up-tree AND it is a different file from self AND the marker is unset; it never
// re-execs itself, an absent pin, or under the loop guard.
func localReexecTarget(cwd, self string, marker bool) (string, bool) {
	if marker {
		return "", false
	}
	root, ok := findDotSatelleRoot(cwd)
	if !ok {
		return "", false
	}
	local := filepath.Join(root, config.DefaultDataDir, localBinaryName)
	fi, err := os.Stat(local)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	if resolvePathOrSelf(local) == resolvePathOrSelf(self) {
		return "", false // we ARE the local pin — run in-process
	}
	return local, true
}

// reexecLocalIfPresent runs the repo-local satelle pin in place of this process
// when one is present (see localReexecTarget). It inherits stdio/args/env (plus
// the loop-guard marker) and returns the child's exit code with handled=true; the
// caller then exits. handled=false means no pin — proceed in-process.
func reexecLocalIfPresent() (code int, handled bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return 0, false
	}
	self, err := os.Executable()
	if err != nil {
		return 0, false
	}
	target, ok := localReexecTarget(cwd, self, os.Getenv(localExecMarker) == "1")
	if !ok {
		return 0, false
	}
	cmd := exec.Command(target, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), localExecMarker+"=1")
	if err := cmd.Run(); err != nil {
		if ee, isExit := err.(*exec.ExitError); isExit {
			return ee.ExitCode(), true
		}
		// Could not launch the pin (e.g. not executable) — fall back to in-process
		// rather than dead-ending the user.
		return 0, false
	}
	return 0, true
}
