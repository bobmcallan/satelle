package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The process of record is reported, never silently replaced (sty_d6e209aa). The
// facts come from config (ProcessFindings, ProcessDivergence); this file is the
// verb-layer seam that says them at engage, in a transition refusal, and in the
// route document. Reports go to stderr or the route text — stdout JSON is never
// touched.

// ProcessProbe carries what the verb layer needs to answer "where is the process
// of record, and does this worktree disagree with it". Wired by the CLI from the
// opened app; nil (a test, or a surface with no app) reports nothing.
type ProcessProbe struct {
	Process      config.Config
	InvokingRoot string
	ProcessRoot  string
}

var processProbe *ProcessProbe

// SetProcessProbe wires the probe. Nil clears it.
func SetProcessProbe(p *ProcessProbe) { processProbe = p }

// processAbsentNotice is the report for an absent authored workflows dir: the
// embedded backstop governs and the story's gates are the binary's defaults.
// Empty when the dir exists (readable or not — unreadable is a refusal, below).
func processAbsentNotice() string {
	if processProbe == nil {
		return ""
	}
	for _, f := range config.ProcessFindings(processProbe.Process, processProbe.ProcessRoot) {
		if f.Kind == config.ProcessAbsent {
			return wfgovern.AbsentMessage(f.Path)
		}
	}
	return ""
}

// processDivergenceNotice is the report for a linked worktree whose own copy of
// the authored process differs from the main tree's. It walks files, so it runs
// only where it is reported — engage, a refusal, the route document — never in
// app.Open or the edit-gate hook.
func processDivergenceNotice() string {
	if processProbe == nil {
		return ""
	}
	return config.DivergenceNotice(processProbe.InvokingRoot, processProbe.ProcessRoot,
		config.ProcessDivergence(processProbe.InvokingRoot, processProbe.ProcessRoot, processProbe.Process))
}

// processCheck refuses when the authored workflows dir exists and cannot be
// read. It is the read surfaces' guard: a stored route document must not be
// served as the repository's route while the process behind it is unreadable.
func processCheck(ctx context.Context) error {
	idx, err := requireDocIndex()
	if err != nil {
		return nil
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return nil
	}
	return wfgovern.RouteSourceOf(wfs).Err()
}

// processNotices are the reports that qualify a story's route: the absent-dir
// backstop notice and the worktree divergence, in that order.
func processNotices() []string {
	var out []string
	if n := processAbsentNotice(); n != "" {
		out = append(out, n)
	}
	if n := processDivergenceNotice(); n != "" {
		out = append(out, n)
	}
	return out
}

// processOfRecordSection renders the `## Process of record` section, or "" when
// there is nothing to report — a healthy repo's route output is unchanged.
func processOfRecordSection() string {
	notices := processNotices()
	if len(notices) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Process of record\n")
	for _, n := range notices {
		b.WriteString("\n- " + strings.ReplaceAll(n, "\n", "\n  ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// reportProcessAtEngage says, once, on stderr, that an engaged story's gates are
// not (or may not be) the repository's. Called on entry into an engaging state
// from a non-engaging one, beside the fresh-session advice. It never refuses and
// never fails: the unreadable case was already refused before the transition.
func reportProcessAtEngage(ctx context.Context, item workitem.Item, from, to string) {
	if item.Kind != workitem.KindStory || ledgerStore == nil {
		return
	}
	clk, ok := clockFor(ctx, item)
	if !ok || !clk.Engaging(to) || clk.Engaging(from) {
		return
	}
	for _, n := range processNotices() {
		EmitVerdict(fmt.Sprintf("satelle: process of record — %s: %s", item.ID, n))
	}
}

// annotateProcessRefusal adds the worktree-divergence report to a refused status
// change, so the operator who is told "that edge is not declared" is also told
// the worktree's own copy of the process is not the one in force. A Refusal keeps
// its type (and its Notes carry the report); any other error is wrapped with %w
// so errors.Is keeps matching. A request that carries no status is not a
// transition and is returned as it came.
func annotateProcessRefusal(raw json.RawMessage, err error) error {
	if err == nil {
		return nil
	}
	var req struct {
		Status *string `json:"status"`
	}
	if json.Unmarshal(raw, &req) != nil || req.Status == nil {
		return err
	}
	note := processDivergenceNotice()
	if note == "" {
		return err
	}
	if r, ok := err.(wfgovern.Refusal); ok {
		r.Notes = append(r.Notes, note)
		return r
	}
	return fmt.Errorf("%w; %s", err, note)
}
