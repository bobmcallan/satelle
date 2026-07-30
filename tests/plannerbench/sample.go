//go:build plannerbench

package plannerbench

import (
	"fmt"
	"sort"
	"strings"
)

// evidenceSchemaVersion 2 adds the full sampleDims dimension set, the
// independent artifact oracle, attempt-aggregated usage, and structured failure
// classification. Schema 1 records are NOT comparable: report.go refuses to mix
// versions rather than coerce them.
const evidenceSchemaVersion = 2

// Topology values. A sample is either an isolated dispatched child or the
// in-loop executor (the driving session itself), never both.
const (
	topologyIsolated = "isolated"
	topologyInLoop   = "in_loop"
)

// Collection values. Isolated samples are instrumented by this harness;
// in-loop samples cannot be spawned by a test (the executor IS the driving
// session) and are ingested as operator attestations. The two are never merged
// into a transport or provider conclusion.
const (
	collectionInstrumented = "instrumented"
	collectionAttested     = "operator-attested"
)

// sampleDims is the dimension set AC1 requires on EVERY sample. Every field is
// either measured or declared by the study; none is inferred at report time.
// report.go compares only cells whose held dimensions are identical, so a
// missing dimension must fail validation rather than silently read as "".
type sampleDims struct {
	StudyID        string `json:"study_id"`
	BindingID      string `json:"binding_id"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	EffortClass    string `json:"effort_class"`
	Interface      string `json:"interface"`
	Topology       string `json:"topology"`
	ToolPolicy     string `json:"tool_policy"`
	ToolGrant      string `json:"tool_grant"`
	Fixture        string `json:"fixture"`
	ContextBytes   int    `json:"context_bytes"`
	ContextBucket  string `json:"context_size_bucket"`
	RunOrder       int    `json:"run_order"`
	Run            int    `json:"run"`
	HarnessVersion string `json:"harness_version"`
	Collection     string `json:"collection"`
}

// dimensionNames are the dimensions a comparison may hold constant. The set is
// closed so a study cannot name a dimension the records do not carry.
var dimensionNames = []string{
	"provider", "model", "effort", "effort_class", "interface", "topology",
	"tool_policy", "tool_grant", "fixture", "context_bytes",
	"context_size_bucket", "binding_id",
}

// value returns one dimension by its study-facing name, and whether the name is
// a known dimension.
func (d sampleDims) value(name string) (string, bool) {
	switch name {
	case "provider":
		return d.Provider, true
	case "model":
		return d.Model, true
	case "effort":
		return d.Effort, true
	case "effort_class":
		return d.EffortClass, true
	case "interface":
		return d.Interface, true
	case "topology":
		return d.Topology, true
	case "tool_policy":
		return d.ToolPolicy, true
	case "tool_grant":
		return d.ToolGrant, true
	case "fixture":
		return d.Fixture, true
	case "context_bytes":
		return fmt.Sprintf("%d", d.ContextBytes), true
	case "context_size_bucket":
		return d.ContextBucket, true
	case "binding_id":
		return d.BindingID, true
	default:
		return "", false
	}
}

// validate reports every dimension a sample failed to record. An unrecorded
// dimension is a harness defect, not a benchmark result: a record that cannot
// say which provider produced it cannot enter any comparison.
func (d sampleDims) validate() []string {
	var findings []string
	required := map[string]string{
		"study_id": d.StudyID, "binding_id": d.BindingID, "provider": d.Provider,
		"model": d.Model, "effort": d.Effort, "effort_class": d.EffortClass,
		"interface": d.Interface, "topology": d.Topology,
		"tool_policy": d.ToolPolicy, "tool_grant": d.ToolGrant,
		"fixture": d.Fixture, "context_size_bucket": d.ContextBucket,
		"harness_version": d.HarnessVersion, "collection": d.Collection,
	}
	for _, name := range sortedKeys(required) {
		if strings.TrimSpace(required[name]) == "" {
			findings = append(findings, "dimension not recorded: "+name)
		}
	}
	if d.ContextBytes <= 0 {
		findings = append(findings, "dimension not recorded: context_bytes")
	}
	if d.RunOrder <= 0 {
		findings = append(findings, "dimension not recorded: run_order")
	}
	if d.Run <= 0 {
		findings = append(findings, "dimension not recorded: run")
	}
	switch d.Topology {
	case topologyIsolated, topologyInLoop:
	default:
		findings = append(findings, fmt.Sprintf(
			"topology %q: want %q or %q", d.Topology, topologyIsolated, topologyInLoop))
	}
	switch d.Collection {
	case collectionInstrumented, collectionAttested:
	default:
		findings = append(findings, fmt.Sprintf(
			"collection %q: want %q or %q", d.Collection, collectionInstrumented, collectionAttested))
	}
	return findings
}

// cellKey identifies the comparable unit — one binding on one fixture. Samples
// sharing a cellKey are the repeated randomized samples AC6 aggregates.
func (d sampleDims) cellKey() string { return d.BindingID + "/" + d.Fixture }

// effortClassFor normalizes a provider-specific effort string to the shared
// class a provider/model comparison holds constant (AC3). A study binding MAY
// declare its own effort_class; this fallback exists only so a binding that
// omits it is not silently classless. Unknown strings stay unknown rather than
// defaulting to a class, so an unmatched pair lands in Confounded instead of
// being compared as if the efforts matched.
func effortClassFor(declared, effort string) string {
	if strings.TrimSpace(declared) != "" {
		return strings.ToLower(strings.TrimSpace(declared))
	}
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "minimal", "low":
		return "low"
	case "medium", "med":
		return "medium"
	case "high", "xhigh", "max":
		return "high"
	default:
		return "unknown"
	}
}

// bucketFor names the context-size bucket a measured byte count falls in, using
// the study's declared thresholds. Thresholds are study data, not Go constants,
// so a repo can re-band without a code change.
func bucketFor(buckets []contextBucket, bytes int) string {
	ordered := append([]contextBucket(nil), buckets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].MaxBytes < ordered[j].MaxBytes })
	for _, b := range ordered {
		if b.MaxBytes > 0 && bytes <= b.MaxBytes {
			return b.Name
		}
	}
	for _, b := range ordered {
		if b.MaxBytes <= 0 {
			return b.Name // the open-ended top bucket
		}
	}
	return "unbucketed"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
