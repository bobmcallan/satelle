package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// Re-checking captured contracts against the installed CLI (sty_32c61e68).
//
// A drift report names the contracts that may no longer hold; this is the way to
// find out which do. It is a command the operator runs on purpose, so it may spawn
// the CLI — through the same bounded, reaping spawn the live probes use — but,
// like them, it starts no session and sends no prompt. A contract that can only be
// re-measured with a paid session is reported unverifiable and names the fixture
// to re-capture; it is never reported as holding.

// RecheckStatus is one contract's outcome.
type RecheckStatus string

const (
	RecheckHolds        RecheckStatus = "holds"
	RecheckChanged      RecheckStatus = "changed"
	RecheckUnverifiable RecheckStatus = "unverifiable"
)

// RecheckLine is one captured contract's status against the installed CLI.
type RecheckLine struct {
	Contract string
	Status   RecheckStatus
	Detail   string
}

// RecheckResult is a harness's re-check.
type RecheckResult struct {
	Harness string
	// HasCapture is false for a harness with no recorded capture: there is
	// nothing to re-check, and the report says so.
	HasCapture bool
	Binary     string
	// Installed is the CLI's reported version, "unknown" when it named none.
	Installed string
	Captured  string
	Lines     []RecheckLine
}

// Changed reports whether any captured contract no longer holds.
func (r RecheckResult) Changed() bool {
	for _, l := range r.Lines {
		if l.Status == RecheckChanged {
			return true
		}
	}
	return false
}

// Render writes the report: a header naming the builds, then one line per
// captured contract, each led by its status.
func (r RecheckResult) Render(w io.Writer) {
	if !r.HasCapture {
		fmt.Fprintf(w, "version drift: no captured version recorded for %s\n", r.Harness)
		return
	}
	fmt.Fprintf(w, "recheck %s: installed %s (%s), captured %s\n", agentcli.CLIName(r.Harness), r.Installed, r.Binary, r.Captured)
	for _, l := range r.Lines {
		fmt.Fprintf(w, "%-12s %s: %s\n", l.Status, l.Contract, l.Detail)
	}
}

// Recheck re-checks harness's captured contracts against the repo's bindings for
// it. The bindings are resolved as `satelle doctor` resolves them; nothing is
// spawned while resolving.
func Recheck(ctx context.Context, o Opts, harness string) RecheckResult {
	o.Live, o.probe = false, nil
	return RecheckGrants(ctx, harness, Check(ctx, o).Grants, o.LiveTimeout)
}

// RecheckGrants re-checks harness's captured contracts given the resolved
// bindings. A harness with no binding is re-checked through its bare CLI name on
// PATH, and the contracts that need a binding are unverifiable.
func RecheckGrants(ctx context.Context, harness string, grants []agentvalidate.Grant, timeout time.Duration) RecheckResult {
	res := RecheckResult{Harness: harness}
	basis, ok := agentcli.CapturedFor(harness)
	if !ok {
		return res
	}
	res.HasCapture, res.Captured = true, basis.Version
	if timeout <= 0 {
		timeout = DefaultLiveTimeout
	}

	var command, acp *agentvalidate.Grant
	for i := range grants {
		g := &grants[i]
		if g.Backend == "in-loop" || g.Backend == "invalid" || strings.TrimSpace(g.Command) == "" ||
			agentcli.HarnessOf(g.Command) != harness {
			continue
		}
		if g.Interface == config.InterfaceACP {
			if acp == nil {
				acp = g
			}
		} else if command == nil {
			command = g
		}
	}
	bin := agentcli.CLIName(harness)
	if g := firstGrant(command, acp); g != nil {
		bin = strings.Fields(g.Command)[0]
	}
	res.Binary = resolvedPath(bin)

	ver := spawnOutput(ctx, bin, timeout, "--version")
	res.Installed = agentcli.NormalizeVersion(ver.out)
	if ver.err != nil || ver.timedOut || res.Installed == "" {
		res.Installed = "unknown"
	}
	help := spawnOutput(ctx, bin, timeout, "--help")
	helpUsable := help.err == nil && !help.timedOut && strings.TrimSpace(help.out) != ""

	for _, c := range basis.Contracts {
		line := RecheckLine{Contract: c.Name}
		switch c.Name {
		case agentcli.ContractEnvelopes:
			line.Status, line.Detail = recheckEnvelopes(harness, help.out, helpUsable, c)
		case agentcli.ContractACPMethods:
			line.Status, line.Detail = recheckACP(ctx, acp, timeout, c)
		default:
			line.Status = RecheckUnverifiable
			line.Detail = "needs a live session to re-measure; re-capture " + c.Fixture
		}
		res.Lines = append(res.Lines, line)
	}
	return res
}

func firstGrant(gs ...*agentvalidate.Grant) *agentvalidate.Grant {
	for _, g := range gs {
		if g != nil {
			return g
		}
	}
	return nil
}

// recheckEnvelopes judges the result-envelope contract from what the installed
// CLI's --help still lists: a template flag that is gone changes the output the
// adapter reads.
func recheckEnvelopes(harness, help string, usable bool, c agentcli.CapturedContract) (RecheckStatus, string) {
	if !usable {
		return RecheckUnverifiable, "the installed CLI did not answer --help; re-capture " + c.Fixture
	}
	var missing []string
	for _, f := range agentcli.RecheckHelpFlags(harness) {
		if !strings.Contains(help, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return RecheckChanged, "--help no longer lists " + strings.Join(missing, ", ") + "; re-capture " + c.Fixture
	}
	return RecheckHolds, "--help lists every flag the command templates use"
}

// recheckACP judges the ACP contract with the live probe's initialize handshake.
func recheckACP(ctx context.Context, g *agentvalidate.Grant, timeout time.Duration, c agentcli.CapturedContract) (RecheckStatus, string) {
	if g == nil {
		return RecheckUnverifiable, "no ACP binding for it in agents.toml to handshake with; re-capture " + c.Fixture
	}
	fs := probeACP(ctx, *g, strings.Fields(g.Command), timeout)
	switch {
	case hasID(fs, health.IDLiveOK):
		return RecheckHolds, "the ACP peer completed initialize (session/* methods are not exercised without a session; re-capture " + c.Fixture + " to re-verify them)"
	case hasID(fs, health.IDLiveACPHandshake):
		return RecheckChanged, "the ACP peer answered but returned no usable initialize result; re-capture " + c.Fixture
	}
	return RecheckUnverifiable, "the ACP peer could not be reached (" + fs[0].Detail + "); re-capture " + c.Fixture
}
