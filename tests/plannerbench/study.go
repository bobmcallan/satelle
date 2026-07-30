//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// A study is DATA (study.json), not Go branches. Adding a provider, a binding,
// or a comparison must never require editing this file — the constitution's
// configuration-over-code rule applied to the benchmark itself. Nothing below
// names a provider.

// contextBucket bands a measured context size. Thresholds are study data so a
// repo can re-band without a code change. Exactly one bucket may carry
// max_bytes 0 — the open-ended top band.
type contextBucket struct {
	Name     string `json:"name"`
	MaxBytes int    `json:"max_bytes"`
}

// toolDivergence records a binding whose tool grant does NOT mirror the shipped
// planner grant (AC11). The divergence is a sample variable, not a footnote:
// report.go holds tool_policy constant, so a divergent binding can never be
// compared against a shipped-grant binding without landing in Confounded.
type toolDivergence struct {
	Shipped string `json:"shipped"`
	Used    string `json:"used"`
	Reason  string `json:"reason"`
}

// studyBinding declares one measurable binding: its dimensions plus the
// agents.toml section the harness writes for it.
type studyBinding struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Effort      string `json:"effort"`
	EffortClass string `json:"effort_class,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Topology    string `json:"topology,omitempty"`
	Command     string `json:"command,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
	// Tools is the binding's tool grant. Empty means "mirror the shipped
	// planner grant" — the default, so the study measures the policy the
	// planner actually ships with.
	Tools string `json:"tools,omitempty"`
	// ToolPolicy names the policy. Reserved names are refused for a diverging
	// binding; see validateBindings.
	ToolPolicy string          `json:"tool_policy,omitempty"`
	Divergence *toolDivergence `json:"tool_policy_divergence,omitempty"`

	// resolved fields, filled by resolveBindings.
	grant       string
	policy      string
	divergence  *toolDivergence
	effortClass string
	topology    string
	iface       string
}

// studyComparison declares one question the study may answer. free_variable is
// the single dimension allowed to differ; holds are the dimensions that must be
// identical across members for the comparison to be readable at all.
type studyComparison struct {
	ID          string   `json:"id"`
	Free        string   `json:"free_variable"`
	Holds       []string `json:"holds"`
	Members     []string `json:"members"`
	Description string   `json:"description,omitempty"`
}

// judgeBinding is the OPTIONAL second oracle (AC8). It must not be the binding
// under test; the deterministic seam oracle is the primary score so a
// credential-less run still yields a real quality signal.
type judgeBinding struct {
	ID        string `json:"id"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Interface string `json:"interface,omitempty"`
	Command   string `json:"command,omitempty"`
	Tools     string `json:"tools,omitempty"`
}

// study is the whole declared experiment.
type study struct {
	ID             string            `json:"id"`
	Seed           int64             `json:"seed"`
	Runs           int               `json:"runs"`
	MinSamples     int               `json:"min_samples"`
	P50GapPercent  float64           `json:"binding_change_p50_gap_percent"`
	ContextBuckets []contextBucket   `json:"context_buckets"`
	Bindings       []studyBinding    `json:"bindings"`
	Comparisons    []studyComparison `json:"comparisons"`
	Judge          *judgeBinding     `json:"judge,omitempty"`

	sha string
}

// shippedPolicyName is the label reserved for a grant that mirrors the shipped
// planner binding verbatim. legacyPolicyName is reserved too: the previous
// harness labelled every binding "read-only" including ones that diverged, the
// exact mislabel AC11 forbids.
const (
	shippedPolicyName = "shipped-planner-grant"
	legacyPolicyName  = "read-only"
)

func loadStudy(path string) (study, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return study{}, err
	}
	var s study
	if err := json.Unmarshal(raw, &s); err != nil {
		return study{}, fmt.Errorf("decode study %s: %w", path, err)
	}
	s.sha = digest(string(raw))
	if err := s.validate(); err != nil {
		return study{}, err
	}
	return s, nil
}

func (s study) validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("study id required")
	}
	if s.Runs < 1 {
		return fmt.Errorf("study runs must be >= 1, got %d", s.Runs)
	}
	if s.MinSamples < 3 {
		return fmt.Errorf("study min_samples must be >= 3 (AC6), got %d", s.MinSamples)
	}
	open := 0
	for _, b := range s.ContextBuckets {
		if b.MaxBytes <= 0 {
			open++
		}
	}
	if len(s.ContextBuckets) == 0 || open != 1 {
		return fmt.Errorf("study needs context_buckets with exactly one open-ended (max_bytes 0) band, got %d of %d",
			open, len(s.ContextBuckets))
	}
	ids := map[string]bool{}
	for _, b := range s.Bindings {
		if ids[b.ID] {
			return fmt.Errorf("duplicate binding id %q", b.ID)
		}
		ids[b.ID] = true
	}
	for _, c := range s.Comparisons {
		if len(c.Members) < 2 {
			return fmt.Errorf("comparison %q needs at least 2 members", c.ID)
		}
		if _, ok := (sampleDims{}).value(c.Free); !ok {
			return fmt.Errorf("comparison %q free_variable %q is not a known dimension (want one of %s)",
				c.ID, c.Free, strings.Join(dimensionNames, ", "))
		}
		for _, hold := range c.Holds {
			if _, ok := (sampleDims{}).value(hold); !ok {
				return fmt.Errorf("comparison %q holds %q which is not a known dimension", c.ID, hold)
			}
			if hold == c.Free {
				return fmt.Errorf("comparison %q holds its own free_variable %q constant", c.ID, c.Free)
			}
		}
		for _, member := range c.Members {
			if !ids[member] {
				return fmt.Errorf("comparison %q names unknown binding %q", c.ID, member)
			}
		}
	}
	return nil
}

// resolveBindings fills each binding's grant, policy label, effort class,
// topology, and interface — and enforces the AC11 divergence rules against the
// grant the planner actually ships with.
func resolveBindings(s study, shippedGrant string) ([]studyBinding, error) {
	resolved := make([]studyBinding, 0, len(s.Bindings))
	for _, b := range s.Bindings {
		b.grant = strings.TrimSpace(b.Tools)
		if b.grant == "" {
			b.grant = shippedGrant
		}
		b.effortClass = effortClassFor(b.EffortClass, b.Effort)
		b.topology = strings.TrimSpace(b.Topology)
		if b.topology == "" {
			b.topology = topologyIsolated
		}
		b.iface = strings.TrimSpace(b.Interface)
		if b.iface == "" {
			b.iface = "command"
		}
		mirrors := b.grant == shippedGrant
		declared := strings.TrimSpace(b.ToolPolicy)
		switch {
		case mirrors:
			// A mirroring binding may only carry the shipped policy name, and
			// needs no divergence record.
			if declared != "" && declared != shippedPolicyName {
				return nil, fmt.Errorf("binding %q mirrors the shipped grant but labels it %q: use %q",
					b.ID, declared, shippedPolicyName)
			}
			if b.Divergence != nil {
				return nil, fmt.Errorf("binding %q mirrors the shipped grant but records a divergence", b.ID)
			}
			b.policy = shippedPolicyName
		default:
			if declared == "" {
				return nil, fmt.Errorf(
					"binding %q diverges from the shipped planner grant and must declare its own tool_policy name", b.ID)
			}
			if declared == shippedPolicyName || strings.EqualFold(declared, legacyPolicyName) {
				return nil, fmt.Errorf(
					"binding %q diverges from the shipped planner grant and may not be labelled %q (reserved)", b.ID, declared)
			}
			if b.Divergence == nil || strings.TrimSpace(b.Divergence.Reason) == "" {
				return nil, fmt.Errorf(
					"binding %q diverges from the shipped planner grant without a recorded tool_policy_divergence.reason", b.ID)
			}
			b.policy = declared
			b.divergence = &toolDivergence{Shipped: shippedGrant, Used: b.grant, Reason: b.Divergence.Reason}
		}
		if b.topology == topologyIsolated && strings.TrimSpace(b.Command) == "" {
			return nil, fmt.Errorf("binding %q is isolated and must declare a command", b.ID)
		}
		resolved = append(resolved, b)
	}
	return resolved, nil
}

// agentsTOML renders the agents.toml the harness writes for one isolated
// binding. [planner] is the section the benchmark workflow's plan node
// allocates; nothing else is written, so the study measures exactly this
// binding.
func (b studyBinding) agentsTOML() string {
	var sb strings.Builder
	sb.WriteString("[planner]\nrole = \"agent\"\n")
	fmt.Fprintf(&sb, "effort = %q\n", b.Effort)
	if b.iface != "command" {
		fmt.Fprintf(&sb, "interface = %q\n", b.iface)
	}
	fmt.Fprintf(&sb, "command = %q\n", b.Command)
	fmt.Fprintf(&sb, "tools = %q\n", b.grant)
	if strings.TrimSpace(b.Model) != "" {
		fmt.Fprintf(&sb, "model = %q\n", b.Model)
	}
	if strings.TrimSpace(b.Timeout) != "" {
		fmt.Fprintf(&sb, "timeout = %q\n", b.Timeout)
	}
	sb.WriteString("principles = \"session\"\n")
	return sb.String()
}

// dims builds the recorded dimension set for one sample of this binding.
func (b studyBinding) dims(s study, fixtureName string, contextBytes, runOrder, run int) sampleDims {
	collection := collectionInstrumented
	if b.topology == topologyInLoop {
		collection = collectionAttested
	}
	return sampleDims{
		StudyID: s.ID, BindingID: b.ID, Provider: b.Provider, Model: b.Model,
		Effort: b.Effort, EffortClass: b.effortClass, Interface: b.iface,
		Topology: b.topology, ToolPolicy: b.policy, ToolGrant: b.grant,
		Fixture: fixtureName, ContextBytes: contextBytes,
		ContextBucket: bucketFor(s.ContextBuckets, contextBytes),
		RunOrder:      runOrder, Run: run,
		HarnessVersion: s.harnessVersion(), Collection: collection,
	}
}

// harnessVersion pins the schema AND the study config, so two records that
// disagree about the experiment cannot be silently pooled.
func (s study) harnessVersion() string {
	return fmt.Sprintf("plannerbench-schema-%d+study-%s", evidenceSchemaVersion, shortSHA(s.sha))
}

// unavailableReason reports why a binding cannot be sampled on this host, or ""
// when it can. Resolution is a filesystem fact (exec.LookPath), not a scan of
// program output — an unresolvable binary is a `spawn` class before any run.
func (b studyBinding) unavailableReason() string {
	if b.topology == topologyInLoop {
		return "in-loop topology: samples are ingested, never spawned"
	}
	fields := strings.Fields(b.Command)
	if len(fields) == 0 {
		return "binding declares no command"
	}
	if _, err := exec.LookPath(fields[0]); err != nil {
		return fmt.Sprintf("%s not on PATH", fields[0])
	}
	return ""
}

func (s study) bindingIDs() []string {
	ids := make([]string, 0, len(s.Bindings))
	for _, b := range s.Bindings {
		ids = append(ids, b.ID)
	}
	sort.Strings(ids)
	return ids
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
