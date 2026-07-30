//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// report.go is PURE: records in, report out. It performs no I/O beyond the final
// write, calls no clock, and spends no tokens — so `make planner-report`
// regenerates a byte-identical report from the durable per-run files, and the
// golden test can pin it.
//
// The discipline this file exists to enforce: a comparison is readable only when
// every dimension it declares held is IDENTICAL across its members. Anything
// else is named as confounded and produces no conclusion. There is no code path
// that emits a provider verdict from unmatched cells.

// Comparison statuses.
const (
	statusSupported    = "supported"
	statusUnderpowered = "underpowered"
	statusConfounded   = "confounded"
	statusSkipped      = "skipped"
)

// cell is one binding on one fixture — the comparable unit AC6 aggregates.
type cell struct {
	Key             string          `json:"key"`
	Dims            sampleDims      `json:"dims"`
	N               int             `json:"n"`
	Metrics         []metricSummary `json:"metrics"`
	UsageAvailable  int             `json:"usage_available_samples"`
	CostAvailable   int             `json:"cost_available_samples"`
	PolicyFaithful  int             `json:"policy_faithful_samples"`
	MirrorsShipped  bool            `json:"mirrors_shipped_grant"`
	JudgeAvailable  int             `json:"judge_available_samples"`
	Classes         map[string]int  `json:"failure_classes,omitempty"`
	CollectionMixed bool            `json:"collection_mixed,omitempty"`
}

func (c cell) metric(name string) (metricSummary, bool) {
	for _, m := range c.Metrics {
		if m.Name == name {
			return m, m.Available
		}
	}
	return metricSummary{Name: name}, false
}

// comparisonResult is one answered — or explicitly unanswered — question.
type comparisonResult struct {
	ID         string   `json:"id"`
	Free       string   `json:"free_variable"`
	Holds      []string `json:"holds"`
	HeldValues []string `json:"held_values"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason,omitempty"`
	Differing  []string `json:"differing_dimensions,omitempty"`
	Sides      []string `json:"sides,omitempty"`
	Conclusion string   `json:"conclusion,omitempty"`
	Mixed      bool     `json:"collection_mixed,omitempty"`
	P50GapPct  *float64 `json:"p50_wall_gap_percent,omitempty"`
	FasterSide string   `json:"faster_side,omitempty"`
	BetterSide string   `json:"higher_quality_side,omitempty"`
}

// reportModel is the whole rendered study.
type reportModel struct {
	StudyID        string             `json:"study_id"`
	HarnessVersion string             `json:"harness_version"`
	Seed           int64              `json:"seed"`
	StudySHA       string             `json:"study_sha256"`
	MinSamples     int                `json:"min_samples"`
	SatelleVersion string             `json:"satelle_version,omitempty"`
	ShippedGrant   shippedGrant       `json:"shipped_planner_grant"`
	Samples        int                `json:"samples"`
	Comparable     int                `json:"comparable_samples"`
	Cells          []cell             `json:"cells"`
	Comparisons    []comparisonResult `json:"comparisons"`
	Recommendation string             `json:"binding_change_recommendation"`
	Problems       []string           `json:"problems,omitempty"`
	Skipped        map[string]string  `json:"skipped_bindings,omitempty"`
}

// metric names, declared once so the cell table and the conclusions cannot
// disagree about what they are reading.
const (
	metricWall     = "wall_ms"
	metricStartup  = "startup_ms"
	metricTTFE     = "ttfe_ms"
	metricTools    = "tool_calls"
	metricAttempts = "attempts"
	metricOracle   = "oracle_coverage_fraction"
	metricTokens   = "tokens_total"
	metricCost     = "cost_total"
)

var reportedMetrics = []string{
	metricWall, metricStartup, metricTTFE, metricTools,
	metricAttempts, metricOracle, metricTokens, metricCost,
}

// buildReport is the pure core. It refuses to mix schema versions rather than
// coerce them: two records that disagree about the schema disagree about what
// their fields mean.
func buildReport(s study, records []runRecord, skipped map[string]string) reportModel {
	model := reportModel{
		StudyID: s.ID, HarnessVersion: s.harnessVersion(), Seed: s.Seed,
		StudySHA: s.sha, MinSamples: s.MinSamples, Samples: len(records),
		Skipped: skipped,
	}
	var usable []runRecord
	for _, record := range records {
		if record.SchemaVersion != evidenceSchemaVersion {
			model.Problems = append(model.Problems, fmt.Sprintf(
				"%s: schema %d refused (this report reads schema %d only)",
				record.RunID, record.SchemaVersion, evidenceSchemaVersion))
			continue
		}
		if findings := record.Dims.validate(); len(findings) > 0 {
			model.Problems = append(model.Problems, fmt.Sprintf(
				"%s: %s", record.RunID, strings.Join(findings, "; ")))
			continue
		}
		if model.SatelleVersion == "" {
			model.SatelleVersion = record.Environment.SatelleVersion
		}
		if model.ShippedGrant.Grant == "" {
			model.ShippedGrant = record.Environment.ShippedGrant
		}
		usable = append(usable, record)
	}
	model.Cells = buildCells(usable)
	for _, c := range model.Cells {
		model.Comparable += c.N
	}
	for _, comparison := range s.Comparisons {
		model.Comparisons = append(model.Comparisons,
			evaluateComparison(comparison, model.Cells, s.MinSamples, skipped)...)
	}
	model.Recommendation = recommendBindingChange(model.Comparisons, s.P50GapPercent)
	sort.Strings(model.Problems)
	return model
}

// buildCells aggregates comparable samples per (binding, fixture).
func buildCells(records []runRecord) []cell {
	grouped := map[string][]runRecord{}
	for _, record := range records {
		if !record.comparable() {
			continue
		}
		grouped[record.Dims.cellKey()] = append(grouped[record.Dims.cellKey()], record)
	}
	cells := make([]cell, 0, len(grouped))
	for key, samples := range grouped {
		cells = append(cells, summarizeCell(key, samples))
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i].Key < cells[j].Key })
	return cells
}

func summarizeCell(key string, samples []runRecord) cell {
	c := cell{Key: key, Dims: samples[0].Dims, N: len(samples), Classes: map[string]int{}}
	c.MirrorsShipped = samples[0].Policy.MirrorsShipped
	values := map[string][]float64{}
	collections := map[string]bool{}
	for _, sample := range samples {
		collections[sample.Dims.Collection] = true
		values[metricWall] = append(values[metricWall], float64(sample.Timing.WallMS))
		if sample.Timing.StartupMS != nil {
			values[metricStartup] = append(values[metricStartup], float64(*sample.Timing.StartupMS))
		}
		if sample.Timing.TTFEMS != nil {
			values[metricTTFE] = append(values[metricTTFE], float64(*sample.Timing.TTFEMS))
		}
		if sample.Tools.Available {
			values[metricTools] = append(values[metricTools], float64(sample.Tools.Calls))
		}
		if sample.Attempts > 0 {
			values[metricAttempts] = append(values[metricAttempts], float64(sample.Attempts))
		}
		values[metricOracle] = append(values[metricOracle], sample.Score.Deterministic.Fraction)
		if sample.Usage.Available {
			c.UsageAvailable++
			if sample.Usage.TokensTotal != nil {
				values[metricTokens] = append(values[metricTokens], float64(*sample.Usage.TokensTotal))
			}
		}
		if sample.Usage.CostTotal != nil {
			c.CostAvailable++
			values[metricCost] = append(values[metricCost], float64(*sample.Usage.CostTotal))
		}
		if sample.Policy.ReadOnlyFaithful {
			c.PolicyFaithful++
		}
		if sample.Score.Judge.Available {
			c.JudgeAvailable++
		}
		c.Classes[sample.Diagnostics.Class]++
	}
	for _, name := range reportedMetrics {
		c.Metrics = append(c.Metrics, summarize(name, values[name]))
	}
	c.CollectionMixed = len(collections) > 1
	return c
}

// evaluateComparison groups a comparison's member cells by their HELD dimension
// values and judges each group. A group whose held values differ from another
// group's is confounded, with the differing dimensions named.
func evaluateComparison(c studyComparison, cells []cell, minSamples int, skipped map[string]string) []comparisonResult {
	members := map[string]bool{}
	for _, id := range c.Members {
		members[id] = true
	}
	var mine []cell
	for _, cl := range cells {
		if members[cl.Dims.BindingID] {
			mine = append(mine, cl)
		}
	}
	if len(mine) == 0 {
		return []comparisonResult{{
			ID: c.ID, Free: c.Free, Holds: c.Holds, Status: statusSkipped,
			Reason: skipReason(c, skipped, "no member binding produced a comparable sample"),
		}}
	}
	groups := map[string][]cell{}
	for _, cl := range mine {
		groups[holdsSignature(cl.Dims, c.Holds)] = append(groups[holdsSignature(cl.Dims, c.Holds)], cl)
	}
	signatures := make([]string, 0, len(groups))
	for sig := range groups {
		signatures = append(signatures, sig)
	}
	sort.Strings(signatures)

	var results []comparisonResult
	for _, sig := range signatures {
		group := groups[sig]
		result := comparisonResult{
			ID: c.ID, Free: c.Free, Holds: c.Holds,
			HeldValues: heldValues(group[0].Dims, c.Holds),
			Sides:      sideLabels(group, c.Free),
		}
		free := distinctFree(group, c.Free)
		switch {
		case len(free) < 2:
			result.Status = statusConfounded
			result.Differing = differingAcrossGroups(groups, signatures, sig, c.Holds)
			if len(result.Differing) > 0 {
				result.Reason = fmt.Sprintf(
					"members do not share the dimensions this comparison holds constant; they differ on %s",
					strings.Join(result.Differing, ", "))
			} else {
				result.Reason = fmt.Sprintf(
					"only one value of the free variable %q is present in this cell group", c.Free)
			}
		case underpowered(group, minSamples):
			result.Status = statusUnderpowered
			result.Reason = underpoweredReason(group, minSamples)
		default:
			result.Status = statusSupported
			result.Mixed = mixedCollection(group)
			result.Conclusion, result.FasterSide, result.BetterSide, result.P50GapPct =
				renderConclusion(c.Free, group, result.Mixed)
		}
		results = append(results, result)
	}
	return results
}

// renderConclusion dispatches to a renderer chosen by the free variable. The
// renderers are SEPARATE functions with no shared template, so a provider
// comparison is structurally incapable of emitting a transport sentence.
func renderConclusion(free string, group []cell, mixed bool) (string, string, string, *float64) {
	switch free {
	case "interface":
		return renderTransportConclusion(group)
	case "provider", "model":
		return renderProviderConclusion(free, group)
	case "topology":
		return renderTopologyConclusion(group, mixed)
	default:
		return renderDimensionConclusion(free, group)
	}
}

// renderTransportConclusion speaks ONLY about transport. It never reads provider
// or model into its text: the two sides share them by construction, so naming
// them would invite the reader to attribute the gap to the provider.
func renderTransportConclusion(group []cell) (string, string, string, *float64) {
	fast, gap := fastestByWall(group)
	quality := highestOracle(group)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Transport comparison over %s: ", strings.Join(sideLabels(group, "interface"), " vs "))
	if fast != "" && gap != nil {
		fmt.Fprintf(&sb, "interface %q completed with the lower p50 wall time (%.1f%% gap)", fast, *gap)
	} else {
		sb.WriteString("no p50 wall-time separation")
	}
	if quality != "" {
		fmt.Fprintf(&sb, "; interface %q scored higher on the independent oracle", quality)
	}
	sb.WriteString(". Provider and model are held constant, so this says nothing about either.")
	return sb.String(), fast, quality, gap
}

// renderProviderConclusion speaks ONLY about the provider or model. It refuses to
// render at all when the group's own held dimensions are not identical — the
// caller only reaches it for a supported group, and this re-check makes the
// invariant local rather than trusting the caller.
func renderProviderConclusion(free string, group []cell) (string, string, string, *float64) {
	if !identicalOn(group, []string{"interface", "tool_policy", "topology", "fixture", "effort_class"}) {
		return "", "", "", nil
	}
	fast, gap := fastestByWall(group)
	quality := highestOracle(group)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s comparison over %s on a fixed interface: ",
		strings.ToUpper(free[:1])+free[1:], strings.Join(sideLabels(group, free), " vs "))
	if quality != "" {
		fmt.Fprintf(&sb, "%s %q scored higher on the independent oracle", free, quality)
	} else {
		sb.WriteString("no oracle separation")
	}
	if fast != "" && gap != nil {
		fmt.Fprintf(&sb, "; %s %q had the lower p50 wall time (%.1f%% gap)", free, fast, *gap)
	}
	sb.WriteString(". Interface, effort class, context, fixture and tool policy are held constant.")
	return sb.String(), fast, quality, gap
}

// renderTopologyConclusion is always qualified: one side is instrumented by this
// harness and the other is an operator attestation, so the sentence says so
// rather than presenting them as like-for-like measurements.
func renderTopologyConclusion(group []cell, mixed bool) (string, string, string, *float64) {
	fast, gap := fastestByWall(group)
	quality := highestOracle(group)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Topology comparison over %s: ", strings.Join(sideLabels(group, "topology"), " vs "))
	if fast != "" && gap != nil {
		fmt.Fprintf(&sb, "topology %q completed with the lower p50 wall time (%.1f%% gap)", fast, *gap)
	} else {
		sb.WriteString("no p50 wall-time separation")
	}
	if quality != "" {
		fmt.Fprintf(&sb, "; topology %q scored higher on the independent oracle", quality)
	}
	sb.WriteString(". ")
	if mixed {
		sb.WriteString("Collection methods DIFFER — attested in-loop samples against instrumented isolated " +
			"samples — so intervention count, carried conversation state and visible progress must be read " +
			"alongside any timing gap. ")
	}
	if !identicalOn(group, []string{"tool_policy"}) {
		sb.WriteString("Tool policy also differs across the sides, which is inherent to the topology " +
			"(the in-loop executor carries the driving session's grant) and is not controlled here. ")
	}
	sb.WriteString("This is a topology result only; it is never merged into a transport or provider conclusion.")
	return sb.String(), fast, quality, gap
}

// renderDimensionConclusion is the neutral renderer for any other free variable.
// It names only the dimension that varied.
func renderDimensionConclusion(free string, group []cell) (string, string, string, *float64) {
	fast, gap := fastestByWall(group)
	quality := highestOracle(group)
	text := fmt.Sprintf("Comparison on %s over %s: ", free, strings.Join(sideLabels(group, free), " vs "))
	if quality != "" {
		text += fmt.Sprintf("%s %q scored higher on the independent oracle", free, quality)
	} else {
		text += "no oracle separation"
	}
	if fast != "" && gap != nil {
		text += fmt.Sprintf("; %s %q had the lower p50 wall time (%.1f%% gap)", free, fast, *gap)
	}
	return text + ". Only this dimension varied.", fast, quality, gap
}

// recommendBindingChange is GATED. It may propose a default-binding change only
// from a supported comparison whose p50 gap clears the study's declared
// threshold. Otherwise it says so — an absence of separation is not a licence to
// pick a side.
func recommendBindingChange(results []comparisonResult, gapPercent float64) string {
	var justified []string
	for _, r := range results {
		if r.Status != statusSupported || r.P50GapPct == nil || r.FasterSide == "" {
			continue
		}
		if r.Mixed {
			continue // attested-vs-instrumented never justifies a default change
		}
		if *r.P50GapPct < gapPercent {
			continue
		}
		justified = append(justified, fmt.Sprintf(
			"%s: changing the default %s to %q is justified by a %.1f%% p50 gap (threshold %.1f%%)",
			r.ID, r.Free, r.FasterSide, *r.P50GapPct, gapPercent))
	}
	if len(justified) == 0 {
		return "no binding change justified by this study"
	}
	sort.Strings(justified)
	return strings.Join(justified, "\n")
}

// --- helpers over cell groups ---

func holdsSignature(d sampleDims, holds []string) string {
	parts := make([]string, 0, len(holds))
	ordered := append([]string(nil), holds...)
	sort.Strings(ordered)
	for _, hold := range ordered {
		v, _ := d.value(hold)
		parts = append(parts, hold+"="+v)
	}
	return strings.Join(parts, "|")
}

func heldValues(d sampleDims, holds []string) []string {
	ordered := append([]string(nil), holds...)
	sort.Strings(ordered)
	values := make([]string, 0, len(ordered))
	for _, hold := range ordered {
		v, _ := d.value(hold)
		values = append(values, hold+"="+v)
	}
	return values
}

// differingAcrossGroups names which held dimensions this group differs on
// relative to any other group of the same comparison — the actionable half of a
// confounded verdict.
func differingAcrossGroups(groups map[string][]cell, signatures []string, mine string, holds []string) []string {
	found := map[string]bool{}
	me := groups[mine][0].Dims
	for _, sig := range signatures {
		if sig == mine {
			continue
		}
		other := groups[sig][0].Dims
		for _, hold := range holds {
			a, _ := me.value(hold)
			b, _ := other.value(hold)
			if a != b {
				found[hold] = true
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func distinctFree(group []cell, free string) []string {
	seen := map[string]bool{}
	for _, c := range group {
		v, _ := c.Dims.value(free)
		seen[v] = true
	}
	values := make([]string, 0, len(seen))
	for v := range seen {
		values = append(values, v)
	}
	sort.Strings(values)
	return values
}

func sideLabels(group []cell, free string) []string {
	labels := make([]string, 0, len(group))
	for _, c := range group {
		v, _ := c.Dims.value(free)
		labels = append(labels, fmt.Sprintf("%s=%s (%s, n=%d)", free, v, c.Dims.BindingID, c.N))
	}
	sort.Strings(labels)
	return labels
}

func underpowered(group []cell, minSamples int) bool {
	for _, c := range group {
		if c.N < minSamples {
			return true
		}
	}
	return false
}

func underpoweredReason(group []cell, minSamples int) string {
	var short []string
	for _, c := range group {
		if c.N < minSamples {
			short = append(short, fmt.Sprintf("%s has n=%d", c.Key, c.N))
		}
	}
	sort.Strings(short)
	return fmt.Sprintf("%s; want n>=%d on every side", strings.Join(short, ", "), minSamples)
}

func mixedCollection(group []cell) bool {
	seen := map[string]bool{}
	for _, c := range group {
		seen[c.Dims.Collection] = true
		if c.CollectionMixed {
			return true
		}
	}
	return len(seen) > 1
}

func identicalOn(group []cell, dims []string) bool {
	for _, dim := range dims {
		first, _ := group[0].Dims.value(dim)
		for _, c := range group[1:] {
			v, _ := c.Dims.value(dim)
			if v != first {
				return false
			}
		}
	}
	return true
}

// fastestByWall returns the free-variable-independent binding label with the
// lowest p50 wall time and the percentage gap to the slowest, or ("", nil) when
// any side lacks the metric.
func fastestByWall(group []cell) (string, *float64) {
	type entry struct {
		label string
		p50   float64
	}
	var entries []entry
	for _, c := range group {
		m, ok := c.metric(metricWall)
		if !ok || m.P50 == nil {
			return "", nil
		}
		entries = append(entries, entry{label: c.Dims.BindingID, p50: *m.P50})
	}
	if len(entries) < 2 {
		return "", nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].p50 == entries[j].p50 {
			return entries[i].label < entries[j].label
		}
		return entries[i].p50 < entries[j].p50
	})
	best, worst := entries[0], entries[len(entries)-1]
	if best.p50 <= 0 {
		return best.label, nil
	}
	gap := (worst.p50 - best.p50) / best.p50 * 100
	return best.label, &gap
}

// highestOracle names the side with the strictly higher p50 oracle coverage, or
// "" when they tie — a tie is not a winner.
func highestOracle(group []cell) string {
	best, bestValue, tied := "", -1.0, false
	for _, c := range group {
		m, ok := c.metric(metricOracle)
		if !ok || m.P50 == nil {
			return ""
		}
		switch {
		case *m.P50 > bestValue:
			best, bestValue, tied = c.Dims.BindingID, *m.P50, false
		case *m.P50 == bestValue:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

func skipReason(c studyComparison, skipped map[string]string, fallback string) string {
	var reasons []string
	for _, member := range c.Members {
		if reason, ok := skipped[member]; ok {
			reasons = append(reasons, member+": "+reason)
		}
	}
	if len(reasons) == 0 {
		return fallback
	}
	sort.Strings(reasons)
	return strings.Join(reasons, "; ")
}

// --- rendering ---

func renderReportMarkdown(m reportModel) string {
	var sb strings.Builder
	sb.WriteString("# Planner benchmark study report\n\n")
	sb.WriteString("## Study\n\n")
	fmt.Fprintf(&sb, "- study: `%s`\n- harness: `%s`\n- seed: `%d`\n- study sha256: `%s`\n",
		m.StudyID, m.HarnessVersion, m.Seed, shortSHA(m.StudySHA))
	fmt.Fprintf(&sb, "- minimum comparable samples per cell: %d\n", m.MinSamples)
	if m.SatelleVersion != "" {
		fmt.Fprintf(&sb, "- satelle: `%s`\n", strings.TrimSpace(m.SatelleVersion))
	}
	fmt.Fprintf(&sb, "- shipped planner grant: `%s` (from `%s`)\n",
		m.ShippedGrant.Grant, m.ShippedGrant.Path)
	fmt.Fprintf(&sb, "- samples: %d recorded, %d comparable\n\n", m.Samples, m.Comparable)

	sb.WriteString("## Cells\n\n")
	if len(m.Cells) == 0 {
		sb.WriteString("_No comparable cells._\n\n")
	} else {
		sb.WriteString("| cell | provider | model | effort | iface | topology | policy | ctx | n | wall p50/p90 | startup p50 | ttfe p50 | tools p50 | attempts p50 | oracle p50 | tokens p50 | usage | cost | policy ok |\n")
		sb.WriteString("|---|---|---|---|---|---|---|---:|---:|---|---|---|---|---|---|---|---:|---:|---:|\n")
		for _, c := range m.Cells {
			fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %d | %d | %d |\n",
				c.Key, c.Dims.Provider, c.Dims.Model, c.Dims.Effort, c.Dims.Interface,
				c.Dims.Topology, c.Dims.ToolPolicy, c.Dims.ContextBucket, c.N,
				pair(c, metricWall), one(c, metricStartup), one(c, metricTTFE),
				one(c, metricTools), one(c, metricAttempts), one(c, metricOracle),
				one(c, metricTokens), c.UsageAvailable, c.CostAvailable, c.PolicyFaithful)
		}
		sb.WriteString("\n`usage`/`cost` count the samples that REPORTED the value; a blank metric is unreported, never zero.\n\n")
	}

	writeSection(&sb, "Supported conclusions", m.Comparisons, statusSupported, true)
	writeSection(&sb, "Underpowered", m.Comparisons, statusUnderpowered, false)
	writeSection(&sb, "Confounded", m.Comparisons, statusConfounded, false)
	writeSection(&sb, "Skipped", m.Comparisons, statusSkipped, false)

	sb.WriteString("## Binding-change recommendation\n\n")
	fmt.Fprintf(&sb, "%s\n\n", m.Recommendation)

	if len(m.Skipped) > 0 {
		sb.WriteString("## Bindings not sampled on this host\n\n")
		for _, id := range sortedKeys(m.Skipped) {
			fmt.Fprintf(&sb, "- `%s`: %s\n", id, m.Skipped[id])
		}
		sb.WriteString("\n")
	}
	if len(m.Problems) > 0 {
		sb.WriteString("## Problems\n\n")
		for _, p := range m.Problems {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("## Honest limits\n\n")
	sb.WriteString("- A conclusion appears above only when every dimension its comparison declares held is " +
		"identical across sides and every side reached the minimum sample count. Everything else is listed " +
		"as confounded, underpowered or skipped and yields no conclusion.\n")
	sb.WriteString("- Cross-provider tool vocabularies are not the same grant. A binding whose grant does not " +
		"mirror the shipped planner grant carries its own `tool_policy` name, so it cannot be compared against " +
		"a shipped-grant binding without landing in Confounded. That is the intended outcome, not a gap to " +
		"paper over.\n")
	sb.WriteString("- In-loop samples are operator attestations: the in-loop executor is the driving session " +
		"and cannot be spawned by a test. Topology results are labelled collection-mixed and never justify a " +
		"default-binding change.\n")
	sb.WriteString("- The artifact score is an independent oracle over the seeded tree, not the transition " +
		"validator. A committed run can score low and a refused run can still be scored.\n")
	return sb.String()
}

func writeSection(sb *strings.Builder, title string, results []comparisonResult, status string, withConclusion bool) {
	fmt.Fprintf(sb, "## %s\n\n", title)
	found := 0
	for _, r := range results {
		if r.Status != status {
			continue
		}
		found++
		fmt.Fprintf(sb, "### %s (free: %s)\n\n", r.ID, r.Free)
		if len(r.Sides) > 0 {
			fmt.Fprintf(sb, "- sides: %s\n", strings.Join(r.Sides, "; "))
		}
		if len(r.HeldValues) > 0 {
			fmt.Fprintf(sb, "- held: %s\n", strings.Join(r.HeldValues, ", "))
		}
		if r.Reason != "" {
			fmt.Fprintf(sb, "- reason: %s\n", r.Reason)
		}
		if withConclusion && r.Conclusion != "" {
			fmt.Fprintf(sb, "- **conclusion:** %s\n", r.Conclusion)
		}
		if r.Mixed {
			fmt.Fprintf(sb, "- collection mixed: yes\n")
		}
		sb.WriteString("\n")
	}
	if found == 0 {
		sb.WriteString("_None._\n\n")
	}
}

func pair(c cell, name string) string {
	m, ok := c.metric(name)
	if !ok || m.P50 == nil || m.P90 == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f / %.0f", *m.P50, *m.P90)
}

func one(c cell, name string) string {
	m, ok := c.metric(name)
	if !ok || m.P50 == nil {
		return "n/a"
	}
	if name == metricOracle {
		return fmt.Sprintf("%.2f", *m.P50)
	}
	return fmt.Sprintf("%.0f", *m.P50)
}

func writeReport(outDir string, m reportModel) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"),
		[]byte(renderReportMarkdown(m)), 0o644)
}
