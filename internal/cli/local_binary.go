// Repo-local satelle binary (sty_fe3ee313): a repo may pin its own satelle under
// .satelle/satelle (installed by `satelle update --local`). When that pin is
// present, it is the binary that runs for the repo — the globally-invoked satelle
// re-execs the local one at startup. This keeps the product repo-agnostic
// (satelle-repo-agnostic): the pin is an installed RELEASE asset, never a source
// build, and nothing about any one repo is baked into the binary.
package cli

import (
	"hash/fnv"
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

// pinRepoRootOf reports whether self is a repo-local pin (a path of the shape
// <root>/.satelle/satelle) and returns that repo root — the PURE core of
// local-mode detection (sty_6b07cfb1). satelle's mode is implicit: a binary
// living in a repo's .satelle/ runs in local mode; any other runs global.
func pinRepoRootOf(self string) (string, bool) {
	if filepath.Base(self) != localBinaryName {
		return "", false
	}
	dir := filepath.Dir(self)
	if filepath.Base(dir) != config.DefaultDataDir {
		return "", false
	}
	return filepath.Dir(dir), true
}

// localPinRepoRoot reports whether THIS running binary is a repo-local pin and,
// if so, the repo root it belongs to — the implicit local-mode signal.
func localPinRepoRoot() (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return "", false
	}
	return pinRepoRootOf(resolvePathOrSelf(self))
}

// localWebPortBase/Span define the deterministic local-mode web-port range
// (8800–8999) — distinct from the global DefaultWebPort (8787).
const (
	localWebPortBase = 8800
	localWebPortSpan = 200
)

// localDeterministicPort maps a repo root to a STABLE web port in the local-mode
// range, so each repo's local instance gets its own predictable port that never
// collides with the global default or (barring hash collisions) other repos. Same
// root → same port.
func localDeterministicPort(repoRoot string) int {
	abs := repoRoot
	if p, err := filepath.Abs(repoRoot); err == nil {
		abs = p
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(abs))
	return localWebPortBase + int(h.Sum32()%uint32(localWebPortSpan))
}

// resolveServePort picks the web port for `serve`: an explicit --port wins, then
// an explicit [web_port] in config, then the local-mode deterministic per-repo
// port, then the global default. Pure, so the precedence is unit-tested.
func resolveServePort(portFlag, configPort int, localRoot string, isLocal bool) int {
	switch {
	case portFlag > 0:
		return portFlag
	case configPort > 0:
		return configPort
	case isLocal:
		return localDeterministicPort(localRoot)
	default:
		return config.DefaultWebPort
	}
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
