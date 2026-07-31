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

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
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

// probeGrant dispatches to the transport-appropriate probe.
func probeGrant(ctx context.Context, g agentvalidate.Grant, timeout time.Duration) health.Findings {
	fields := strings.Fields(g.Command)
	if len(fields) == 0 {
		return nil // the deterministic pass already reported this
	}
	if g.Interface == config.InterfaceACP {
		return probeACP(ctx, g, fields, timeout)
	}
	return probeCommand(ctx, g, fields, timeout)
}

// probeCommand asks a command-transport CLI for its version. It is the cheapest
// call that proves the binary exists, starts, and is not wedged — without a
// session or a prompt.
func probeCommand(ctx context.Context, g agentvalidate.Grant, fields []string, timeout time.Duration) health.Findings {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, fields[0], "--version")
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start).Round(time.Millisecond)

	if ctx.Err() != nil {
		return health.Findings{health.Warn(health.IDLiveTimeout, "Live probe timed out",
			fmt.Sprintf("[%s] %s did not answer --version within %s (process killed)", g.Name, fields[0], timeout)).
			About(g.Name).WithRemediation("check the CLI starts by hand, or raise --timeout")}
	}
	if err != nil {
		if reason := authFailure(string(out)); reason != "" {
			return health.Findings{health.Warn(health.IDLiveAuth, "Provider authentication",
				fmt.Sprintf("[%s] %s reports an authentication problem: %s", g.Name, fields[0], reason)).
				About(g.Name).WithRemediation("authenticate the CLI itself (satelle never stores agent credentials)")}
		}
		return health.Findings{health.Warn(health.IDLiveSpawn, "Live probe failed",
			fmt.Sprintf("[%s] %s --version failed: %v", g.Name, fields[0], err)).
			About(g.Name).WithRemediation("verify the binary in the [" + g.Name + "] command template")}
	}
	return health.Findings{health.Info(health.IDLiveOK, "Provider reachable",
		fmt.Sprintf("[%s] %s answered in %s", g.Name, fields[0], elapsed)).About(g.Name)}
}

// probeACP spawns an ACP peer, sends a single `initialize` request over stdio,
// waits for the response, then cancels. No `session/new`, no prompt — so the
// handshake is proven without a paid call.
func probeACP(ctx context.Context, g agentvalidate.Grant, fields []string, timeout time.Duration) health.Findings {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }

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
		_ = killGroup(cmd)
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
