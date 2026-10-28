package doctor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/procgroup"
)

// Live provider probes — OPT-IN ONLY (`satelle doctor --live`).
//
// Ordinary doctor spawns no provider and touches no network. These probes do
// spawn provider processes, so they may consume the operator's provider auth and
// rate budget; none of them starts a session or sends a prompt, so none makes a
// paid model call.
//
// Every probe is bounded and REAPS. A leaked provider process is the failure
// mode worth designing against: each spawn gets its own process group, is killed
// on deadline or parent cancellation, and is Wait()ed on every exit path.

// probeGrants runs one probe per isolated binding, concurrently but bounded, and
// returns only after every probe has reaped its process.
func probeGrants(ctx context.Context, o Opts, grants []agentvalidate.Grant) health.Findings {
	timeout := o.LiveTimeout
	if timeout <= 0 {
		timeout = DefaultLiveTimeout
	}
	probe := o.probe
	if probe == nil {
		probe = probeGrant
	}

	type result struct {
		i  int
		fs health.Findings
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		out  []result
		sema = make(chan struct{}, 4)
	)
	for i, g := range grants {
		if g.Backend == "in-loop" || g.Backend == "invalid" {
			continue // nothing to spawn
		}
		wg.Add(1)
		go func(i int, g agentvalidate.Grant) {
			defer wg.Done()
			sema <- struct{}{}
			defer func() { <-sema }()
			fs := probe(ctx, g, timeout)
			mu.Lock()
			out = append(out, result{i, fs})
			mu.Unlock()
		}(i, g)
	}
	wg.Wait()

	// Deterministic order: probes finish in whatever order the OS allows, but a
	// report that reorders between runs is unusable as evidence.
	sort.Slice(out, func(a, b int) bool { return out[a].i < out[b].i })
	var fs health.Findings
	for _, r := range out {
		fs = append(fs, r.fs...)
	}
	return fs
}

// probeGrant dispatches to the transport-appropriate probe, then reports the
// binding's CLI build against the one satelle's measured behavior was captured
// against (sty_32c61e68) — on either interface, since the drift is in the CLI,
// not in how it is reached.
func probeGrant(ctx context.Context, g agentvalidate.Grant, timeout time.Duration) health.Findings {
	fields := strings.Fields(g.Command)
	if len(fields) == 0 {
		return nil // the deterministic pass already reported this
	}
	harness := agentcli.HarnessOf(g.Command)
	var fs health.Findings
	if g.Interface == config.InterfaceACP {
		fs = probeACP(ctx, g, fields, timeout)
		if _, ok := agentcli.CapturedVersion(harness); ok && !hasID(fs, health.IDLiveSpawn) {
			// An ACP peer is never asked for --version, and the initialize result
			// carries no build string in the captured fixtures, so the same binary
			// is asked once, bounded and reaped like any other probe.
			fs = append(fs, acpVersionFindings(ctx, g, fields, timeout)...)
		}
	} else {
		// command and stream: cheapest proof the binary exists is --version.
		// stream does not open a session at probe time (sty_d244fe1b).
		fs = probeCommand(ctx, g, fields, timeout)
	}
	if harness != agentcli.HarnessUnknown {
		if _, ok := agentcli.CapturedVersion(harness); !ok {
			fs = append(fs, health.Info(health.IDLiveVersionDrift, "No captured version recorded",
				fmt.Sprintf("[%s] version drift: no captured version recorded for %s", g.Name, harness)).About(g.Name))
		}
	}
	return fs
}

func hasID(fs health.Findings, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}

// spawned is the outcome of one bounded provider spawn.
type spawned struct {
	out      string
	err      error
	timedOut bool
	elapsed  time.Duration
}

// spawnOutput runs bin with args and returns its combined output. It is the one
// spawn path the one-shot probes share: its own process group, killed on the
// deadline or on parent cancellation, and reaped on every exit.
func spawnOutput(ctx context.Context, bin string, timeout time.Duration, args ...string) spawned {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, args...)
	procgroup.Set(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	out, err := cmd.CombinedOutput()
	return spawned{out: string(out), err: err, timedOut: ctx.Err() != nil, elapsed: time.Since(start).Round(time.Millisecond)}
}

// probeCommand asks a command-transport CLI for its version. It is the cheapest
// call that proves the binary exists, starts, and is not wedged — without a
// session or a prompt. The version it reads is compared with the captured one.
func probeCommand(ctx context.Context, g agentvalidate.Grant, fields []string, timeout time.Duration) health.Findings {
	r := spawnOutput(ctx, fields[0], timeout, "--version")
	unknown := versionUnknownNote(g)

	if r.timedOut {
		return health.Findings{health.Warn(health.IDLiveTimeout, "Live probe timed out",
			fmt.Sprintf("[%s] %s did not answer --version within %s (process killed)%s", g.Name, fields[0], timeout, unknown)).
			About(g.Name).WithRemediation("check the CLI starts by hand, or raise --timeout")}
	}
	if r.err != nil {
		if reason := authFailure(r.out); reason != "" {
			return health.Findings{health.Warn(health.IDLiveAuth, "Provider authentication",
				fmt.Sprintf("[%s] %s reports an authentication problem: %s%s", g.Name, fields[0], reason, unknown)).
				About(g.Name).WithRemediation("authenticate the CLI itself (satelle never stores agent credentials)")}
		}
		return health.Findings{health.Warn(health.IDLiveSpawn, "Live probe failed",
			fmt.Sprintf("[%s] %s --version failed: %v%s", g.Name, fields[0], r.err, unknown)).
			About(g.Name).WithRemediation("verify the binary in the [" + g.Name + "] command template")}
	}
	fs := health.Findings{health.Info(health.IDLiveOK, "Provider reachable",
		fmt.Sprintf("[%s] %s answered in %s", g.Name, fields[0], r.elapsed)).About(g.Name)}
	return append(fs, versionFindings(g, fields, agentcli.NormalizeVersion(r.out))...)
}

// versionUnknownNote is the suffix a failed --version probe carries for a CLI
// whose captured build is recorded: the comparison could not be made, and the
// report says so rather than passing it by.
func versionUnknownNote(g agentvalidate.Grant) string {
	if _, ok := agentcli.CapturedVersion(agentcli.HarnessOf(g.Command)); !ok {
		return ""
	}
	return " — installed version unknown, so it cannot be compared with the captured one"
}

// acpVersionFindings reads an ACP binding's CLI build with a bounded --version
// spawn and compares it with the captured one.
func acpVersionFindings(ctx context.Context, g agentvalidate.Grant, fields []string, timeout time.Duration) health.Findings {
	r := spawnOutput(ctx, fields[0], timeout, "--version")
	if r.timedOut || r.err != nil {
		reason := "did not answer"
		if r.err != nil && !r.timedOut {
			reason = fmt.Sprintf("failed: %v", r.err)
		}
		return health.Findings{unknownVersion(g, fields, fmt.Sprintf("%s --version %s", fields[0], reason))}
	}
	return versionFindings(g, fields, agentcli.NormalizeVersion(r.out))
}

// versionFindings compares a binding's installed CLI build with the one its
// harness's measured behavior was captured against. It answers nothing for a
// harness with no recorded capture (probeGrant reports that by name), nothing for
// a match, a WARN naming both builds, the resolved binary and the contracts at
// risk for a difference, and a WARN for output that named no version at all.
func versionFindings(g agentvalidate.Grant, fields []string, installed string) health.Findings {
	harness := agentcli.HarnessOf(g.Command)
	captured, ok := agentcli.CapturedVersion(harness)
	if !ok {
		return nil
	}
	if installed == "" {
		return health.Findings{unknownVersion(g, fields, fields[0]+" --version printed no version")}
	}
	if installed == captured {
		return nil
	}
	return health.Findings{health.Warn(health.IDLiveVersionDrift, "CLI version differs from the captured one",
		fmt.Sprintf("[%s] %s (resolved to %s)", g.Name, agentcli.DriftSummary(harness, installed), resolvedPath(fields[0]))).
		About(g.Name).WithRemediation("run `" + agentcli.RecheckCommand(harness) + "`")}
}

func unknownVersion(g agentvalidate.Grant, fields []string, why string) health.Finding {
	harness := agentcli.HarnessOf(g.Command)
	captured, _ := agentcli.CapturedVersion(harness)
	return health.Warn(health.IDLiveVersionDrift, "CLI version unknown",
		fmt.Sprintf("[%s] version unknown: %s (resolved to %s), so it cannot be compared with %s, the build satelle's measured behavior was captured against. Captured contracts that may no longer hold: %s",
			g.Name, why, resolvedPath(fields[0]), captured, agentcli.DriftContracts(harness))).
		About(g.Name).WithRemediation("run `" + agentcli.RecheckCommand(harness) + "`")
}

// resolvedPath is the binary a bare command name resolves to on this PATH — the
// one a dispatch runs, which matters when several installs share a PATH.
func resolvedPath(bin string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	return bin
}

// probeACP spawns an ACP peer, sends a single `initialize` request over stdio,
// waits for the response, then cancels. No `session/new`, no prompt — so the
// handshake is proven without a paid call.
func probeACP(ctx context.Context, g agentvalidate.Grant, fields []string, timeout time.Duration) health.Findings {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	procgroup.Set(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return acpSpawnFailure(g, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return acpSpawnFailure(g, err)
	}
	if err := cmd.Start(); err != nil {
		return acpSpawnFailure(g, err)
	}
	// Reap on every exit path — a leaked provider process is the failure mode.
	defer func() {
		_ = procgroup.Kill(cmd)
		_ = cmd.Wait()
	}()

	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": 1},
	})
	if _, err := stdin.Write(append(req, '\n')); err != nil {
		return acpSpawnFailure(g, err)
	}

	answered := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var msg struct {
				ID     *int64          `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.ID == nil {
				continue // notification or noise — keep reading for the response
			}
			answered <- len(msg.Error) == 0
			return
		}
		answered <- false
	}()

	select {
	case ok := <-answered:
		if !ok {
			return health.Findings{health.Warn(health.IDLiveACPHandshake, "ACP handshake failed",
				fmt.Sprintf("[%s] %s accepted the connection but returned no usable initialize result", g.Name, fields[0])).
				About(g.Name).WithRemediation("check the ACP adapter version for [" + g.Name + "]")}
		}
		return health.Findings{health.Info(health.IDLiveOK, "ACP peer reachable",
			fmt.Sprintf("[%s] %s completed initialize in %s", g.Name, fields[0], time.Since(start).Round(time.Millisecond))).
			About(g.Name)}
	case <-ctx.Done():
		return health.Findings{health.Warn(health.IDLiveTimeout, "Live probe timed out",
			fmt.Sprintf("[%s] %s did not complete the ACP initialize handshake within %s (process killed)", g.Name, fields[0], timeout)).
			About(g.Name).WithRemediation("check the ACP adapter starts by hand, or raise --timeout")}
	}
}

func acpSpawnFailure(g agentvalidate.Grant, err error) health.Findings {
	return health.Findings{health.Warn(health.IDLiveSpawn, "ACP peer would not start",
		fmt.Sprintf("[%s] spawn failed: %v", g.Name, err)).
		About(g.Name).WithRemediation("verify the acp spawn line on [" + g.Name + "]")}
}

// authFailure returns a short reason when output carries an OBSERVABLE
// authentication signal. Authentication is diagnosed only where the provider
// says so — never inferred from a generic non-zero exit, because reporting a
// wrong cause is worse than reporting none.
func authFailure(out string) string {
	lower := strings.ToLower(out)
	for _, marker := range []string{"not logged in", "unauthorized", "authentication failed", "please log in", "please login", "401"} {
		if strings.Contains(lower, marker) {
			return marker
		}
	}
	return ""
}
