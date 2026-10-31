package substratereview

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Dispositions the judge may return for an item.
const (
	// DispRemove: the default covers it.
	DispRemove = "remove"
	// DispReduce: keep only the named lines.
	DispReduce = "reduce"
	// DispKeep: it carries a named quality rule the default lacks.
	DispKeep = "keep"
	// DispConflict: it contradicts the constitution or the BDD baseline.
	DispConflict = "conflict"
	// NotJudged is rendered for an item or rule the judge returned nothing for.
	NotJudged = "not judged"
)

func validDisposition(d string) bool {
	return d == DispRemove || d == DispReduce || d == DispKeep || d == DispConflict
}

// ItemJudgement is the judge's disposition of one item.
type ItemJudgement struct {
	Item        string   `json:"item"`
	Disposition string   `json:"disposition"`
	KeptLines   []string `json:"kept_lines,omitempty"`
	Rule        string   `json:"rule,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// RuleFinding is the judge's finding against one constitution rule: a file and
// line in the codebase that contradicts it, or None when it found none.
type RuleFinding struct {
	Rule string `json:"rule"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	None bool   `json:"none,omitempty"`
	Note string `json:"note,omitempty"`
}

// Judgement is what the reviewer seat returns.
type Judgement struct {
	Items        []ItemJudgement `json:"items"`
	Constitution []RuleFinding   `json:"constitution_findings"`
}

// ParseJudgement reads the judgement out of a reviewer's notes: the first JSON
// object in them that decodes as a Judgement. Prose around it is ignored.
func ParseJudgement(notes string) (Judgement, error) {
	for i := strings.IndexByte(notes, '{'); i >= 0; {
		dec := json.NewDecoder(strings.NewReader(notes[i:]))
		var j Judgement
		if err := dec.Decode(&j); err == nil && (j.Items != nil || j.Constitution != nil) {
			return j, nil
		}
		next := strings.IndexByte(notes[i+1:], '{')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return Judgement{}, fmt.Errorf("the reviewer's notes carry no judgement object {\"items\":[…],\"constitution_findings\":[…]}")
}

// JudgeInput is the material the judge receives.
type JudgeInput struct {
	Items             []JudgeItem `json:"items"`
	Rules             []Rule      `json:"rules"`
	ConstitutionPath  string      `json:"constitution_path,omitempty"`
	OrchestratorModel string      `json:"orchestrator_model,omitempty"`
}

// JudgeItem is one item with its evidence, as the judge sees it.
type JudgeItem struct {
	Item
	Evidence Evidence `json:"evidence"`
}

// ReportItem is one item's row in the report.
type ReportItem struct {
	Item
	Disposition string   `json:"disposition"`
	KeptLines   []string `json:"kept_lines,omitempty"`
	Rule        string   `json:"rule,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Evidence    Evidence `json:"evidence"`
}

// ReportRule is one constitution rule's row in the report.
type ReportRule struct {
	Rule
	// Finding is "finding", "no finding" or NotJudged.
	Finding string `json:"finding"`
	File    string `json:"file,omitempty"`
	FLine   int    `json:"finding_line,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Report is the whole review.
type Report struct {
	GeneratedAt       time.Time         `json:"generated_at"`
	Binding           string            `json:"binding,omitempty"`
	OrchestratorModel string            `json:"orchestrator_model,omitempty"`
	SeatModels        map[string]string `json:"seat_models,omitempty"`
	Items             []ReportItem      `json:"items"`
	Rules             []ReportRule      `json:"constitution"`
}

// Build joins the inventory, its evidence and the judgement. Every item and
// every rule appears; one the judge omitted (or gave an unknown disposition)
// renders NotJudged.
func Build(in JudgeInput, j Judgement) Report {
	byItem := map[string]ItemJudgement{}
	for _, ij := range j.Items {
		byItem[ij.Item] = ij
	}
	byRule := map[string]RuleFinding{}
	for _, f := range j.Constitution {
		byRule[f.Rule] = f
	}
	rep := Report{OrchestratorModel: in.OrchestratorModel}
	for _, it := range in.Items {
		ri := ReportItem{Item: it.Item, Evidence: it.Evidence, Disposition: NotJudged}
		if ij, ok := byItem[it.Key]; ok && validDisposition(ij.Disposition) {
			ri.Disposition, ri.KeptLines, ri.Rule, ri.Reason = ij.Disposition, ij.KeptLines, ij.Rule, ij.Reason
		}
		rep.Items = append(rep.Items, ri)
	}
	for _, r := range in.Rules {
		rr := ReportRule{Rule: r, Finding: NotJudged}
		if f, ok := byRule[r.Name]; ok {
			switch {
			case f.File != "":
				rr.Finding, rr.File, rr.FLine, rr.Note = "finding", f.File, f.Line, f.Note
			case f.None:
				rr.Finding, rr.Note = "no finding", f.Note
			}
		}
		rep.Rules = append(rep.Rules, rr)
	}
	return rep
}

// WriteJSON writes the report to path.
func (r Report) WriteJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadReport reads a report written by WriteJSON.
func ReadReport(path string) (Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return Report{}, fmt.Errorf("%s is not a substrate review report: %w", path, err)
	}
	return r, nil
}

// Find returns the report's row for an item key.
func (r Report) Find(key string) (ReportItem, bool) {
	for _, it := range r.Items {
		if it.Key == key {
			return it, true
		}
	}
	return ReportItem{}, false
}

// Render writes the report as markdown.
func (r Report) Render(out io.Writer) {
	fmt.Fprintln(out, "# Substrate review")
	fmt.Fprintln(out)
	orch := r.OrchestratorModel
	if orch == "" {
		orch = "not recorded"
	}
	fmt.Fprintf(out, "Orchestrator model (latest in-loop session): %s\n", orch)
	if r.Binding != "" {
		fmt.Fprintf(out, "Judged by binding: %s\n", r.Binding)
	}
	counts := map[string]int{}
	for _, it := range r.Items {
		counts[it.Disposition]++
	}
	fmt.Fprintf(out, "Items: %d — remove %d, reduce %d, keep %d, conflict %d, not judged %d\n\n",
		len(r.Items), counts[DispRemove], counts[DispReduce], counts[DispKeep], counts[DispConflict], counts[NotJudged])

	fmt.Fprintln(out, "## Items")
	for _, it := range r.Items {
		fmt.Fprintf(out, "\n### %s — %s\n\n", it.Key, it.Disposition)
		fmt.Fprintf(out, "- origin: %s (%s)\n", it.Origin, it.Source)
		if it.Reason != "" {
			fmt.Fprintf(out, "- reason: %s\n", oneLine(it.Reason))
		}
		if it.Rule != "" {
			fmt.Fprintf(out, "- rule: %s\n", it.Rule)
		}
		for _, l := range it.KeptLines {
			fmt.Fprintf(out, "- kept: %s\n", oneLine(l))
		}
		for _, l := range it.Evidence.Lines() {
			fmt.Fprintf(out, "- %s\n", l)
		}
	}

	fmt.Fprintln(out, "\n## Constitution")
	fmt.Fprintln(out)
	for _, rr := range r.Rules {
		where := rr.Origin
		if rr.Line > 0 {
			where = fmt.Sprintf("constitution:%d", rr.Line)
		}
		line := fmt.Sprintf("- %s (%s): %s", rr.Name, where, rr.Finding)
		if rr.File != "" {
			line += fmt.Sprintf(" %s:%d", rr.File, rr.FLine)
		}
		if rr.Note != "" {
			line += " — " + oneLine(rr.Note)
		}
		fmt.Fprintln(out, line)
	}
}

// Lines renders the evidence as report lines.
func (e Evidence) Lines() []string {
	var out []string
	out = append(out, fmt.Sprintf("history: %d presentation(s), %d rejection(s); amended %d (%.0f%%), reversed %d (%.0f%%), re-presented to accept %d (%.0f%%); parks %d; rework %d; flaky %d",
		e.Presentations, e.Rejections, e.Amended, e.AmendedShare, e.Reversed, e.ReversedShare, e.Represented, e.RepresentedShare, e.Parks, e.Rework, e.Flaky))
	for _, r := range e.Reasons {
		out = append(out, "rejected: "+r)
	}
	switch {
	case !e.DissentRecorded:
		out = append(out, "dissent: not recorded")
	case len(e.Dissent) == 0:
		out = append(out, "dissent: none")
	}
	for _, d := range e.Dissent {
		verdict := "rejected"
		if d.Accepted {
			verdict = "accepted"
		}
		out = append(out, fmt.Sprintf("dissent: %s %s %s under %s — %s", d.Story, d.Seat, verdict, d.Model, d.Notes))
	}
	for _, m := range e.Models {
		out = append(out, fmt.Sprintf("model %s: %d invocation(s), %d accept(s), %d rejection(s) — %s",
			m.Model, m.Invocations, m.Accepts, m.Rejections, strings.Join(m.Stories, ", ")))
	}
	for _, seat := range sortedKeys(e.CurrentModels) {
		m := e.CurrentModels[seat]
		if m == "" {
			m = "(binding names none)"
		}
		out = append(out, fmt.Sprintf("seat %s is bound to %s", seat, m))
	}
	if len(e.Stories) > 0 {
		out = append(out, "stories: "+strings.Join(e.Stories, ", "))
	}
	return out
}
