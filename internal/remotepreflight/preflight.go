// Package remotepreflight runs the security preflight a remote definition
// declares, before the remote starts: the probes each step says must succeed or
// must be refused, a scan of the host's environment and files for credentials,
// and the rule that no step holds data, untrusted-input and outbound legs
// together.
//
// The package compares outcomes with what the definition declared and nothing
// else. Which probes, which services and what counts as refused are the author's
// argv; it names no host, vendor or provider. It reaches the host only through
// the Host it is given, and it never records a value a host printed — only key
// names, paths and exit codes.
package remotepreflight

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// Kinds of check a preflight makes.
const (
	KindProbe = "probe"
	KindEnv   = "env"
	KindFile  = "file"
	KindLegs  = "legs"
)

// Outcomes a check ends with.
const (
	OutcomePass   = "pass"
	OutcomeFail   = "fail"
	OutcomeError  = "error" // the host could not run the command at all
	OutcomeNotRun = "not-run"
)

// Outcome is what a Host did with one command. Ran is false when the host chose
// not to execute it (the stub host), in which case Note says why.
type Outcome struct {
	Ran    bool
	Exit   int
	Stdout string
	Note   string
}

// Host runs an argv where the remote will run. An error means the command could
// not be launched; a command that ran and failed is a non-zero Exit.
type Host interface {
	Exec(ctx context.Context, argv []string) (Outcome, error)
}

// Check is one judgement of the preflight. It carries no value a host printed.
type Check struct {
	Step    string   `json:"step,omitempty"`
	Kind    string   `json:"kind"`
	Argv    []string `json:"argv,omitempty"`
	Expect  string   `json:"expect,omitempty"`
	Outcome string   `json:"outcome"`
	Pass    bool     `json:"pass"`
	Detail  string   `json:"detail,omitempty"`
}

// Result is every check, in the order they ran, and whether all passed.
type Result struct {
	Checks []Check `json:"checks"`
	Passed bool    `json:"passed"`
}

// Run makes every check the definition declares against h. Its contract is a
// Definition that remotedef.Validate has already accepted: probe form and step
// names are judged there and not again here.
//
// The launcher's order is host.start, then Run; on a result that has not Passed
// it runs host.stop and refuses to start the remote.
//
// Checks come in this order: each step's probes in definition order, the
// environment scan, the file scan, then the legs rule.
func Run(ctx context.Context, def remotedef.Definition, h Host) Result {
	var checks []Check
	for _, s := range def.Steps {
		for _, p := range s.Probes {
			checks = append(checks, probeCheck(ctx, h, s.Name, p))
		}
	}
	checks = append(checks, envChecks(ctx, h, def.Preflight)...)
	checks = append(checks, fileChecks(ctx, h, def.Preflight)...)
	for _, name := range def.StepsHoldingAllLegs() {
		checks = append(checks, Check{
			Step: name, Kind: KindLegs, Outcome: OutcomeFail,
			Detail: fmt.Sprintf("step holds all three legs (%s, %s, %s); drop at least one",
				remotedef.LegData, remotedef.LegUntrusted, remotedef.LegOutbound),
		})
	}
	res := Result{Checks: checks, Passed: true}
	for _, c := range checks {
		res.Passed = res.Passed && c.Pass
	}
	return res
}

func probeCheck(ctx context.Context, h Host, step string, p remotedef.Probe) Check {
	c := Check{Step: step, Kind: KindProbe, Argv: p.Argv, Expect: p.Expect}
	out, err := h.Exec(ctx, p.Argv)
	switch {
	case err != nil:
		c.Outcome, c.Detail = OutcomeError, "could not run: "+err.Error()
	case !out.Ran:
		c.Outcome, c.Pass, c.Detail = OutcomeNotRun, true, out.Note
	case p.Expect == remotedef.ExpectSucceed && out.Exit == 0,
		p.Expect == remotedef.ExpectRefused && out.Exit != 0:
		c.Outcome, c.Pass, c.Detail = OutcomePass, true, fmt.Sprintf("exit %d", out.Exit)
	default:
		c.Outcome, c.Detail = OutcomeFail, fmt.Sprintf("exit %d", out.Exit)
	}
	return c
}

// scan runs a host-wide probe whose stdout is read by parse, which returns the
// credentials found, one detail each. A host that did not run the probe is a
// passing not-run check; a definition that declares none has no scan to make.
func scan(ctx context.Context, h Host, kind string, argv []string, parse func(stdout string) []string) []Check {
	if len(argv) == 0 {
		return nil
	}
	out, err := h.Exec(ctx, argv)
	switch {
	case err != nil:
		return []Check{{Kind: kind, Argv: argv, Outcome: OutcomeError, Detail: "could not run: " + err.Error()}}
	case !out.Ran:
		return []Check{{Kind: kind, Argv: argv, Outcome: OutcomeNotRun, Pass: true, Detail: out.Note}}
	case out.Exit != 0:
		return []Check{{Kind: kind, Argv: argv, Outcome: OutcomeFail, Detail: fmt.Sprintf("probe exited %d", out.Exit)}}
	}
	found := parse(out.Stdout)
	if len(found) == 0 {
		return []Check{{Kind: kind, Argv: argv, Outcome: OutcomePass, Pass: true, Detail: "no credential found"}}
	}
	checks := make([]Check, 0, len(found))
	for _, d := range found {
		checks = append(checks, Check{Kind: kind, Argv: argv, Outcome: OutcomeFail, Detail: d})
	}
	return checks
}

// envChecks fails on each credential-shaped KEY=VALUE in the host's environment
// that the definition does not allow. The value is never kept.
func envChecks(ctx context.Context, h Host, pf remotedef.Preflight) []Check {
	return scan(ctx, h, KindEnv, pf.EnvProbe, func(stdout string) []string {
		var found []string
		for _, line := range strings.Split(stdout, "\n") {
			key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || !config.SecretishKey(key) || !config.CarriesValue(val) || slices.Contains(pf.AllowCredentials, key) {
				continue
			}
			found = append(found, "credential in the environment: "+key)
		}
		return found
	})
}

// fileChecks fails on each path the file probe prints that the definition does
// not allow.
func fileChecks(ctx context.Context, h Host, pf remotedef.Preflight) []Check {
	return scan(ctx, h, KindFile, pf.FileProbe, func(stdout string) []string {
		var found []string
		for _, line := range strings.Split(stdout, "\n") {
			path := strings.TrimSpace(line)
			if path == "" || slices.Contains(pf.AllowCredentials, path) {
				continue
			}
			found = append(found, "credential file on the host: "+path)
		}
		return found
	})
}

// Describe is a check as one line: where it is, then why. A refusal and a
// printed result both say it, so the two cannot disagree. A probe that ran
// states what the definition expected beside what it got, pass or fail.
func Describe(c Check) string {
	where := c.Kind
	if c.Step != "" {
		where = c.Step + " " + c.Kind
	}
	if len(c.Argv) > 0 {
		where += " " + strings.Join(c.Argv, " ")
	}
	detail := c.Detail
	if c.Expect != "" && (c.Outcome == OutcomePass || c.Outcome == OutcomeFail) {
		detail = fmt.Sprintf("expected %s, got %s", c.Expect, detail)
	}
	return fmt.Sprintf("%s: %s (%s)", where, detail, c.Outcome)
}

// LedgerRow is the remote_preflight row for one check of remote, written under
// actor and scoped to storyID. Like the check, it carries key names, paths and
// exit codes and never a value a host printed.
func LedgerRow(remote, storyID, actor string, c Check) ledger.AppendInput {
	payload, _ := json.Marshal(map[string]any{
		"remote": remote, "step": c.Step, "kind": c.Kind, "argv": c.Argv, "expect": c.Expect,
		"outcome": c.Outcome, "pass": c.Pass, "detail": c.Detail,
	})
	return ledger.AppendInput{
		StoryID: storyID, Kind: ledger.KindRemotePreflight, Actor: actor,
		Body:    fmt.Sprintf("remote %s preflight %s: %s", remote, Describe(c), c.Outcome),
		Payload: payload,
	}
}
