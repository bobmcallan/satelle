//go:build integration

package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scripts/install.sh is the published `curl | sh` first-touch (release.yml copies
// it verbatim to the release asset), so its runtime behaviour is only observable
// by RUNNING it. These tests drive the real script — unmodified — against a
// canned `curl` placed earlier on PATH, so they are hermetic (no network) and
// deterministic. SATELLE_INSTALL_DIR points at a temp dir, so a test run never
// touches the operator's installed binary.
//
// The defect under regression (sty_87b5d4bc): both GitHub release lookups used
// `curl … | grep -m1 …`. grep exits on its first match, curl takes EPIPE
// mid-body and prints `curl: (23) Failure writing output to destination` — so
// the FIRST line a new user ever saw was an error, above satelle's own output,
// on an install that then succeeded. The fix writes each API body to a file and
// greps the file.

// installStub writes a POSIX-sh `curl` stub into dir and returns dir. The stub
// serves canned responses for every URL install.sh requests — nothing reaches
// the network — and varies one path per mode:
//
//	success        every request succeeds
//	transport_fail the release-lookup host does not resolve (curl exit 6)
//	no_tag         the lookup returns 200 with a body carrying no tag_name
//	sha_mismatch   the CLI .sha256 does not match the downloaded asset
//	serve_404      the satelle-serve asset 404s (the soft-fail branch)
func installStub(t *testing.T, dir, mode, wantSHA string) {
	t.Helper()
	stub := fmt.Sprintf(`#!/bin/sh
# Canned curl for install.sh tests. No network. $STUB_MODE selects the variant.
url=""; out=""; prev=""
for a in "$@"; do
	case "$prev" in -o) out="$a" ;; esac
	case "$a" in http*) url="$a" ;; esac
	prev="$a"
done

# emit BODY to -o's file, or to stdout when the caller gave no -o. Writing to
# stdout is what makes this stub able to REPRODUCE the defect: the API bodies
# below are padded to a few KB, as the real releases JSON is, so a truncating
# reader on the other end of a pipe (grep -m1) closes early, this process takes
# EPIPE mid-write, and curl reports exit 23 "Failure writing output to
# destination". A stub that only ever wrote a 3-line body could not fail the way
# production did, and the runtime tests here would pass against the old code.
#
# Ignoring SIGPIPE is what lets this stub REPORT the truncated write the way
# curl does. Without the trap the shell is killed by the signal and dies
# silently, so the stub could not emit curl's diagnostic and a runtime test
# would see clean stderr even against the broken script.
trap '' PIPE
emit() {
	if [ -n "$out" ]; then
		printf '%%s' "$1" >"$out"
	elif ! printf '%%s' "$1" 2>/dev/null; then
		echo "curl: (23) Failure writing output to destination" >&2
		exit 23
	fi
}
# The body must exceed the OS pipe buffer (64 KiB on Linux), otherwise a piped
# writer completes into the buffer and never sees EPIPE — the defect would not
# reproduce and these tests would pass against the broken script.
pad=$(awk 'BEGIN{for(i=0;i<4000;i++) printf "  \"filler_%%d\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\n", i}')

case "$url" in
*api.github.com/repos/*/releases/latest)
	if [ "$STUB_MODE" = transport_fail ]; then
		echo "curl: (6) Could not resolve host: api.github.com" >&2
		exit 6
	fi
	if [ "$STUB_MODE" = no_tag ]; then
		emit "{
  \"message\": \"ok\",
$pad  \"id\": 123
}
"
		exit 0
	fi
	emit "{
  \"tag_name\": \"v9.9.9\",
$pad  \"name\": \"v9.9.9\"
}
"
	exit 0 ;;
*api.github.com/repos/*/releases?per_page=*)
	emit "{
  \"tag_name\": \"v9.9.9\",
  \"tag_name\": \"serve-v8.8.8\",
$pad  \"end\": true
}
"
	exit 0 ;;
*/satelle-serve-*.sha256)
	printf '%%s  satelle-serve\n' "%[2]s" >"$out"; exit 0 ;;
*/satelle-serve-*)
	if [ "$STUB_MODE" = serve_404 ]; then
		echo "curl: (22) The requested URL returned error: 404" >&2
		exit 22
	fi
	printf 'SERVE-BINARY\n' >"$out"; exit 0 ;;
*/satelle-v*.sha256)
	if [ "$STUB_MODE" = sha_mismatch ]; then
		printf '%%s  satelle\n' "0000000000000000000000000000000000000000000000000000000000000000" >"$out"
		exit 0
	fi
	printf '%%s  satelle\n' "%[1]s" >"$out"; exit 0 ;;
*/satelle-v*)
	printf 'CLI-BINARY\n' >"$out"; exit 0 ;;
esac
echo "stub curl: unexpected url $url" >&2
exit 99
`, wantSHA, hashOf("SERVE-BINARY\n"))
	path := filepath.Join(dir, "curl")
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatalf("write curl stub: %v", err)
	}
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// runInstall executes scripts/install.sh under the stub for one mode, returning
// stdout, stderr, the exit code, and the install dir.
func runInstall(t *testing.T, mode string) (stdout, stderr string, code int, installDir string) {
	t.Helper()
	script := filepath.Join(repoRootForTest(), "scripts", "install.sh")
	stubDir := t.TempDir()
	installStub(t, stubDir, mode, hashOf("CLI-BINARY\n"))
	installDir = t.TempDir()

	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_MODE="+mode,
		"SATELLE_INSTALL_DIR="+installDir,
	)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run install.sh (%s): %v", mode, err)
		}
	}
	return outBuf.String(), errBuf.String(), code, installDir
}

// TestInstallScriptSuccessIsSilentOnStderr is the direct regression for the
// reported defect (AC1): a successful install emits NOTHING on stderr, and the
// first line of stdout is satelle's own status line — not a curl error.
func TestInstallScriptSuccessIsSilentOnStderr(t *testing.T) {
	stdout, stderr, code, dir := runInstall(t, "success")
	if code != 0 {
		t.Fatalf("success exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("success must emit nothing on stderr, got:\n%s", stderr)
	}
	first := strings.SplitN(strings.TrimLeft(stdout, "\n"), "\n", 2)[0]
	if !strings.HasPrefix(first, "satelle install: fetching ") {
		t.Errorf("first stdout line = %q, want the satelle fetching line", first)
	}
	for _, name := range []string{"satelle", "satelle-serve"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("expected %s to be installed: %v", name, err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable (mode %v)", name, info.Mode())
		}
	}
}

// TestInstallScriptNamesBothTheNewRepoAndExistingRepos (sty_0f471251 AC1):
// installing and upgrading are the same command and the script cannot reliably
// tell which one it just did, so it names BOTH cases rather than guessing. A
// fresh install ignores the second block; an upgrade — which has just
// invalidated the scaffolding of every repo already on the machine — finally
// gets told, where previously the closing guidance was only ever about the one
// repo the operator was about to create.
func TestInstallScriptNamesBothTheNewRepoAndExistingRepos(t *testing.T) {
	stdout, stderr, code, _ := runInstall(t, "success")
	if code != 0 {
		t.Fatalf("success exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"Next (a new repo):",
		"satelle init ",
		"satelle service install",
		"Repos you already have",
		"satelle doctor --all",
		"satelle init --all",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("installer guidance must contain %q, got:\n%s", want, stdout)
		}
	}
	// The upgrade block must come AFTER the new-repo block: a first-time user
	// reads top-down and the new repo is their case.
	if i, j := strings.Index(stdout, "Next (a new repo):"), strings.Index(stdout, "Repos you already have"); i < 0 || j < 0 || i > j {
		t.Errorf("the new-repo block must precede the existing-repos block:\n%s", stdout)
	}
}

// TestInstallScriptNoCurlFeedsAPipe is the STATIC half of the regression (AC3):
// the defect is reintroduced the moment any curl in the script writes into a
// pipe, whether or not a runtime test happens to cover that lookup. `||` is not
// a pipe, and the header comments legitimately show the `curl … | sh` one-liner,
// so both are excluded.
func TestInstallScriptNoCurlFeedsAPipe(t *testing.T) {
	path := filepath.Join(repoRootForTest(), "scripts", "install.sh")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	// Join line-continuations first: the original defect wrote `curl …\` on one
	// line and `| grep -m1 …` on the next, so a per-physical-line scan misses it
	// entirely — the mistake this test is here to catch.
	var logical []string
	var cur strings.Builder
	firstLineOf := map[int]int{}
	for i, line := range strings.Split(string(body), "\n") {
		if cur.Len() == 0 {
			firstLineOf[len(logical)] = i + 1
		}
		if strings.HasSuffix(line, `\`) {
			cur.WriteString(strings.TrimSuffix(line, `\`))
			continue
		}
		cur.WriteString(line)
		logical = append(logical, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		logical = append(logical, cur.String())
	}

	pipe := regexp.MustCompile(`curl[^|]*\|[^|]`)
	for n, stmt := range logical {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if pipe.MatchString(stmt) {
			t.Errorf("scripts/install.sh:%d feeds curl into a pipe — a truncating "+
				"reader (grep -m1, head -1) makes curl report exit 23 %q on a "+
				"successful install (sty_87b5d4bc). Write the body to a file with "+
				"-o and read the file:\n\t%s",
				firstLineOf[n], "Failure writing output to destination", trimmed)
		}
	}
}

// TestInstallScriptTransportFailureIsDiagnosable (AC2): a genuine transport
// failure must still surface curl's own message and exit non-zero — the fix must
// not have been "silence curl's stderr". Nothing may be installed.
func TestInstallScriptTransportFailureIsDiagnosable(t *testing.T) {
	stdout, stderr, code, dir := runInstall(t, "transport_fail")
	if code == 0 {
		t.Fatalf("transport failure must exit non-zero\nstdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "curl: (6)") {
		t.Errorf("transport failure must keep curl's own diagnostic on stderr, got:\n%s", stderr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("nothing may be installed on a failed lookup, found %d entries", len(entries))
	}
}

// TestInstallScriptHandledFailuresKeepExitOne (AC4): the two failure paths the
// script handles itself keep exit status 1 and their own messages — the guards
// sit on lines the fix did not touch, so this is a regression check.
func TestInstallScriptHandledFailuresKeepExitOne(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"no_tag", "could not resolve latest release"},
		{"sha_mismatch", "sha256 mismatch"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			stdout, stderr, code, dir := runInstall(t, tc.mode)
			if code != 1 {
				t.Errorf("%s exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", tc.mode, code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("%s stderr should contain %q, got:\n%s", tc.mode, tc.want, stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "satelle")); err == nil {
				t.Errorf("%s must not leave an installed binary", tc.mode)
			}
		})
	}
}

// TestInstallScriptServeSoftFailPreserved (AC5): the serve lookup is
// deliberately soft-fail — a missing serve asset warns, leaves a working CLI
// install, and still exits 0. De-piping that branch must not have converted it
// into a hard failure.
func TestInstallScriptServeSoftFailPreserved(t *testing.T) {
	stdout, stderr, code, dir := runInstall(t, "serve_404")
	if code != 0 {
		t.Fatalf("serve soft-fail must still exit 0, got %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "fall back to satelle serve") {
		t.Errorf("expected the serve fallback warning, got:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "satelle")); err != nil {
		t.Errorf("the CLI must remain installed when serve is unavailable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "satelle-serve")); err == nil {
		t.Errorf("satelle-serve must not be installed when its asset 404s")
	}
}
