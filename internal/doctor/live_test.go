package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// fakeBin writes an executable script into a temp dir and returns its path.
// Live probes are exercised against real processes — a probe that only ever ran
// against a mock would not prove that it reaps.
func fakeBin(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func grantFor(name, command, iface string) agentvalidate.Grant {
	return agentvalidate.Grant{Name: name, Command: command, Interface: iface, Backend: "isolated:test"}
}

// TestOrdinaryDoctorNeverProbes is AC3's most important guarantee, asserted the
// only way that means anything: a probe hook that FAILS the test if it is ever
// called. Ordinary doctor must spawn no provider and make no network call.
func TestOrdinaryDoctorNeverProbes(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	Check(context.Background(), Opts{
		RepoRoot: root,
		DataDir:  filepath.Join(root, ".satelle"),
		// Live is deliberately NOT set.
		probe: func(context.Context, agentvalidate.Grant, time.Duration) health.Findings {
			t.Fatal("ordinary doctor must never run a live probe")
			return nil
		},
	})
}

// TestLiveProbeRunsOnlyWhenAskedFor pins the flag: the same fixture probes
// exactly once per isolated binding when --live is set.
func TestLiveProbeRunsOnlyWhenAskedFor(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	var calls int
	Check(context.Background(), Opts{
		RepoRoot: root,
		DataDir:  filepath.Join(root, ".satelle"),
		Live:     true,
		probe: func(context.Context, agentvalidate.Grant, time.Duration) health.Findings {
			calls++
			return nil
		},
	})
	if calls != 1 {
		t.Errorf("want one probe for the single isolated binding, got %d", calls)
	}
}

// TestProbeCommandReachable pins the happy path: a CLI that answers --version is
// reported reachable, as INFO — a passing probe is context, not a problem.
func TestProbeCommandReachable(t *testing.T) {
	bin := fakeBin(t, "fake-cli", `echo "fake-cli 1.2.3"; exit 0`)
	fs := probeGrant(context.Background(), grantFor("judge", bin+" {system}", config.InterfaceCommand), 5*time.Second)
	if len(fs) != 1 || fs[0].ID != health.IDLiveOK || fs[0].Severity != health.SeverityInfo {
		t.Fatalf("want one info %s finding, got %+v", health.IDLiveOK, fs)
	}
}

// TestProbeCommandAuthFailure pins that authentication is diagnosed only where
// the provider SAYS so — an observable marker, never inferred from a bare
// non-zero exit.
func TestProbeCommandAuthFailure(t *testing.T) {
	authBin := fakeBin(t, "auth-cli", `echo "error: not logged in" >&2; exit 1`)
	fs := probeGrant(context.Background(), grantFor("judge", authBin+" {system}", config.InterfaceCommand), 5*time.Second)
	if len(fs) != 1 || fs[0].ID != health.IDLiveAuth {
		t.Fatalf("want a %s finding, got %+v", health.IDLiveAuth, fs)
	}

	// A failure with no auth marker must NOT be reported as an auth problem.
	plainBin := fakeBin(t, "broken-cli", `echo "segfault" >&2; exit 3`)
	fs = probeGrant(context.Background(), grantFor("judge", plainBin+" {system}", config.InterfaceCommand), 5*time.Second)
	if len(fs) != 1 || fs[0].ID != health.IDLiveSpawn {
		t.Fatalf("an unexplained failure must not be labelled auth: %+v", fs)
	}
}

// TestProbeCommandTimeoutReapsTheProcess pins the deadline AND the cleanup. A
// leaked provider process is the failure mode this design guards against, so the
// test asserts the process is gone, not merely that the probe returned.
func TestProbeCommandTimeoutReapsTheProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	bin := fakeBin(t, "slow-cli", `echo $$ > `+marker+`; sleep 30`)

	start := time.Now()
	fs := probeGrant(context.Background(), grantFor("judge", bin+" {system}", config.InterfaceCommand), 300*time.Millisecond)
	elapsed := time.Since(start)

	if len(fs) != 1 || fs[0].ID != health.IDLiveTimeout {
		t.Fatalf("want a %s finding, got %+v", health.IDLiveTimeout, fs)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the probe must return at its deadline, took %s", elapsed)
	}
	assertReaped(t, marker)
}

// TestProbeACPHandshakeFailure pins the ACP diagnosis: a peer that accepts the
// pipe but never completes initialize is a handshake failure, distinct from a
// timeout and from a spawn failure.
func TestProbeACPHandshakeFailure(t *testing.T) {
	// Reads the request, then closes stdout without answering.
	bin := fakeBin(t, "acp-mute", `head -n 1 >/dev/null; exit 0`)
	fs := probeGrant(context.Background(), grantFor("judge", bin, config.InterfaceACP), 5*time.Second)
	if len(fs) != 1 || fs[0].ID != health.IDLiveACPHandshake {
		t.Fatalf("want a %s finding, got %+v", health.IDLiveACPHandshake, fs)
	}
}

// TestProbeACPReachable pins the ACP happy path: a peer that answers initialize
// is reachable, with no session and no prompt sent.
func TestProbeACPReachable(t *testing.T) {
	bin := fakeBin(t, "acp-ok", `head -n 1 >/dev/null; echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'; sleep 1`)
	fs := probeGrant(context.Background(), grantFor("judge", bin, config.InterfaceACP), 5*time.Second)
	if len(fs) != 1 || fs[0].ID != health.IDLiveOK {
		t.Fatalf("want a %s finding, got %+v", health.IDLiveOK, fs)
	}
}

// TestProbeACPTimeoutReapsTheProcess pins the ACP deadline and cleanup: a peer
// that accepts the connection and then hangs is killed, not leaked.
func TestProbeACPTimeoutReapsTheProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	bin := fakeBin(t, "acp-hang", `echo $$ > `+marker+`; sleep 30`)

	fs := probeGrant(context.Background(), grantFor("judge", bin, config.InterfaceACP), 300*time.Millisecond)
	if len(fs) != 1 || fs[0].ID != health.IDLiveTimeout {
		t.Fatalf("want a %s finding, got %+v", health.IDLiveTimeout, fs)
	}
	assertReaped(t, marker)
}

// TestProbeACPSpawnFailure pins the third ACP outcome: a peer that cannot start
// at all is a spawn failure, not a handshake one.
func TestProbeACPSpawnFailure(t *testing.T) {
	fs := probeGrant(context.Background(),
		grantFor("judge", "/definitely/not/a/binary", config.InterfaceACP), 2*time.Second)
	if len(fs) != 1 || fs[0].ID != health.IDLiveSpawn {
		t.Fatalf("want a %s finding, got %+v", health.IDLiveSpawn, fs)
	}
}

// TestLiveFindingsAreNeverBlocking pins the severity choice: a live probe is
// evidence about the world, not a defect in the repo's configuration, so it must
// not by itself make a repo unhealthy.
func TestLiveFindingsAreNeverBlocking(t *testing.T) {
	root := newFixtureRepo(t, fixtureOpts{})
	r := Check(context.Background(), Opts{
		RepoRoot: root,
		DataDir:  filepath.Join(root, ".satelle"),
		Live:     true,
		probe: func(context.Context, agentvalidate.Grant, time.Duration) health.Findings {
			return health.Findings{health.Warn(health.IDLiveTimeout, "t", "the provider did not answer")}
		},
	})
	if !r.OK {
		t.Error("a failed live probe must not make a well-configured repo unhealthy")
	}
	if !ids(r)[health.IDLiveTimeout] {
		t.Error("the probe finding must still be reported")
	}
}

// assertReaped fails when the pid recorded in marker is still alive shortly
// after the probe returned.
func assertReaped(t *testing.T, marker string) {
	t.Helper()
	var pid string
	for i := 0; i < 50; i++ {
		if b, err := os.ReadFile(marker); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			pid = strings.TrimSpace(string(b))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == "" {
		t.Skip("the fake process never recorded its pid — nothing to assert about reaping")
	}
	// Give the kill a moment to land, then confirm the process is gone.
	for i := 0; i < 50; i++ {
		if err := exec.Command("kill", "-0", pid).Run(); err != nil {
			return // gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("live probe leaked process %s — it must be killed and reaped", pid)
}
