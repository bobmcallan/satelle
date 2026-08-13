// Package health carries the shared FINDING vocabulary — stable identifiers,
// severity, and remediation — that every satelle validation surface speaks
// (sty_e9da28e2).
//
// Before this package, each surface rendered its own prose: `satelle agent
// validate` printed `FAIL <sentence>`, engagement refused with a differently
// worded sentence, and `satelle init` printed a third. An operator could not
// tell that all three were the same defect, and nothing could be matched
// mechanically. A Finding gives one defect one identifier wherever it surfaces.
//
// It is a LEAF: no satelle imports, so `agentvalidate`, `doctor`, `cli`, and
// `verb` can all speak it without a cycle. It carries no rule of its own — it is
// a vocabulary, not a validator. Deciding whether something IS a defect stays
// with the authority that owns that rule.
package health

import (
	"fmt"
	"sort"
	"strings"
)

// Severity orders a finding's consequence. The mapping to exit codes and to
// fail-closed behaviour is the CALLER's, so a surface can treat a warning as
// advisory while another treats the same finding as blocking.
type Severity string

const (
	// SeverityError is a hard defect: a non-zero exit, and a refused engagement.
	SeverityError Severity = "error"
	// SeverityWarn is advisory — printed, never blocking. Used where the
	// underlying test is a heuristic and a false positive would strand a repo.
	SeverityWarn Severity = "warn"
	// SeverityInfo is context an operator asked for (a live probe that passed).
	SeverityInfo Severity = "info"
)

// rank orders severities for Worst().
func (s Severity) rank() int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarn:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// AtLeast reports whether s is at least as severe as other.
func (s Severity) AtLeast(other Severity) bool { return s.rank() >= other.rank() }

// Stable finding identifiers. They are a CONTRACT: an operator scripts against
// them, and two surfaces reporting the same defect must use the same id. Add a
// new id rather than repurposing one whose meaning has shifted.
const (
	// Agents layer / profile resolution.
	IDAgentsLoad          = "agents.load"           // agents.toml absent or unparseable
	IDAgentsBinding       = "agents.binding"        // a binding is invalid (command, interface, timeout, …)
	IDAgentsProfileBroken = "agents.profile.broken" // a machine-wide profile reference does not resolve
	IDEnvUnresolved       = "env.unresolved"        // a ${VAR} in env/settings resolves to nothing

	// Reviewer / allocation safety.
	IDReviewerUnsafe = "reviewer.unsafe"       // a reviewer's permission ceiling is escaped
	IDNodeAlloc      = "node.alloc"            // a workflow node/edge allocates a binding that does not resolve
	IDHookAlloc      = "hook.alloc.unresolved" // a lifecycle hook's agent allocation is unusable

	// Required binaries.
	IDBinaryMissing   = "binary.missing"   // a binding's executable is not on PATH
	IDBinaryMalformed = "binary.malformed" // a binding's command cannot be parsed into argv

	// Authored substrate.
	IDWorkflowStructure   = "workflow.structure"   // a workflow/skill/principle/task fails its structure contract
	IDWorkflowConsistency = "workflow.consistency" // cross-workflow consistency (ambiguity, unresolved skills)

	// Deployment.
	IDScaffoldStale   = "scaffold.stale"   // deployed harness scaffolding differs from the binary's canonical form
	IDScaffoldMissing = "scaffold.missing" // canonical harness scaffolding is absent
	IDRepoUnreadable  = "repo.unreadable"  // a registered repo could not be checked at all
	IDConfigStray     = "config.stray"     // machine-scope key leftover in a repo file (sty_21a7d16d)

	// Live probes (opt-in only).
	IDLiveOK           = "live.ok"            // a provider answered
	IDLiveAuth         = "live.auth"          // the provider reported an authentication problem
	IDLiveTimeout      = "live.timeout"       // the probe exceeded its deadline
	IDLiveSpawn        = "live.spawn"         // the provider process could not be started
	IDLiveACPHandshake = "live.acp.handshake" // an ACP peer accepted the pipe but never completed initialize
)

// ids is the registry every constant above belongs to — the uniqueness test
// reads it, so a copy-pasted duplicate fails the build's tests rather than
// silently collapsing two defects into one identifier.
var ids = []string{
	IDAgentsLoad, IDAgentsBinding, IDAgentsProfileBroken, IDEnvUnresolved,
	IDReviewerUnsafe, IDNodeAlloc, IDHookAlloc,
	IDBinaryMissing, IDBinaryMalformed,
	IDWorkflowStructure, IDWorkflowConsistency,
	IDScaffoldStale, IDScaffoldMissing, IDRepoUnreadable, IDConfigStray,
	IDLiveOK, IDLiveAuth, IDLiveTimeout, IDLiveSpawn, IDLiveACPHandshake,
}

// IDs returns every declared finding identifier, sorted.
func IDs() []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// Finding is one health observation.
//
// Detail is the operator-facing sentence — the SAME text a surface would have
// printed on its own, so adopting findings changes identifiers and structure
// without rewording established messages. It must never contain a secret value;
// name an environment key, never its contents.
type Finding struct {
	ID          string   `json:"id"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	Remediation string   `json:"remediation,omitempty"`
	// Artifact is what the finding is about — a file path, a binding section, a
	// workflow name. Free-form and repo-relative where it is a path.
	Artifact string `json:"artifact,omitempty"`
}

// Error builds an error-severity finding.
func Error(id, title, detail string) Finding {
	return Finding{ID: id, Severity: SeverityError, Title: title, Detail: detail}
}

// Warn builds a warn-severity finding.
func Warn(id, title, detail string) Finding {
	return Finding{ID: id, Severity: SeverityWarn, Title: title, Detail: detail}
}

// Info builds an info-severity finding.
func Info(id, title, detail string) Finding {
	return Finding{ID: id, Severity: SeverityInfo, Title: title, Detail: detail}
}

// WithRemediation returns f carrying the fix to suggest.
func (f Finding) WithRemediation(s string) Finding { f.Remediation = s; return f }

// About returns f carrying the artifact it concerns.
func (f Finding) About(artifact string) Finding { f.Artifact = artifact; return f }

// String renders one finding as `id — detail (remediation)`.
func (f Finding) String() string {
	s := f.ID + " — " + f.Detail
	if f.Remediation != "" {
		s += " (" + f.Remediation + ")"
	}
	return s
}

// Findings is an ordered list of observations.
type Findings []Finding

// Worst returns the highest severity present, or "" when empty.
func (f Findings) Worst() Severity {
	worst := Severity("")
	for _, x := range f {
		if x.Severity.rank() > worst.rank() {
			worst = x.Severity
		}
	}
	return worst
}

// OK reports whether nothing rises to an error.
func (f Findings) OK() bool { return f.Worst() != SeverityError }

// WithSeverity returns only the findings at one severity.
func (f Findings) WithSeverity(s Severity) Findings {
	var out Findings
	for _, x := range f {
		if x.Severity == s {
			out = append(out, x)
		}
	}
	return out
}

// Details returns the operator-facing sentence of each finding at severity s —
// the compatibility seam for surfaces that still carry plain string lists.
func (f Findings) Details(s Severity) []string {
	var out []string
	for _, x := range f {
		if x.Severity == s {
			out = append(out, x.Detail)
		}
	}
	return out
}

// RenderRefusal formats an error-severity set for a fail-closed message —
// the shared shape a refusal uses so an operator sees the same identifiers a
// diagnostic would print. Returns "" when nothing rises to an error.
func RenderRefusal(prefix string, f Findings) string {
	errs := f.WithSeverity(SeverityError)
	if len(errs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d problem(s)):", prefix, len(errs))
	for _, x := range errs {
		fmt.Fprintf(&b, "\n  - %s", x.String())
	}
	return b.String()
}
