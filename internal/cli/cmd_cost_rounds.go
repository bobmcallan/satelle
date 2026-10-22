package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/verb"
)

// printReviewRounds renders `story cost --rounds`: for each gated edge the
// presentation count and, per seat attempt, its turns, tool calls, outcome and
// duration. A figure the harness did not give prints "—" with the reason, never
// a zero. For an epic it then adds each child's rounds and the family total per
// edge.
func printReviewRounds(cmd *cobra.Command, rr verb.ReviewRounds) error {
	out := cmd.OutOrStdout()
	if len(rr.Edges) == 0 {
		fmt.Fprintf(out, "%s: no gated edge has been presented\n", rr.StoryID)
	} else if err := printStoryRounds(cmd, rr.ReviewRoundsReport); err != nil {
		return err
	}
	if rr.Family == nil {
		return nil
	}
	for _, s := range rr.Family.Stories {
		if s.StoryID == rr.StoryID || len(s.Edges) == 0 {
			continue
		}
		fmt.Fprintln(out)
		if err := printStoryRounds(cmd, s); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "\nFAMILY TOTAL %s — attempts by outcome; turns and tool calls sum available figures only; CONVERGED, STOPPED and OPEN count the stories' edges\n", rr.Family.RootID)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EDGE\tPRESENTATIONS\tACCEPTED\tREJECTED\tNO VERDICT\tACCEPT\tREJECT\tUNAVAILABLE\tMAX-TURNS\tTURNS\tTOOL CALLS\tPERF TURNS\tPERF TOKENS IN/OUT\tPERF DURATION\tPRESENTED\tANSWERED\tADOPTED\tREBUTTED\tCONVERGED\tSTOPPED\tOPEN")
	for _, t := range rr.Family.Total {
		fmt.Fprintf(w, "%s→%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%d/%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
			t.From, t.To, t.Presentations, t.Accepted, t.Rejected, t.NoVerdict,
			t.Attempts.Accept, t.Attempts.Reject, t.Attempts.Unavailable, t.Attempts.MaxTurns,
			summedFigure(t.Turns, t.UnavailableTurns), summedFigure(t.ToolCalls, t.UnavailableToolCalls),
			summedFigure(t.PerformerTurns, t.UnavailablePerformerTurns), t.PerformerTokensIn, t.PerformerTokensOut, fmtDurationMs(t.PerformerDurationMs),
			t.FindingsPresented, t.Answered, t.Adopted, t.Rebutted, t.Converged, t.Stopped, t.Open)
	}
	return w.Flush()
}

// summedFigure renders a sum over available figures, naming how many attempts
// gave none so the sum is not read as complete.
func summedFigure(sum, unavailable int) string {
	if unavailable == 0 {
		return fmt.Sprint(sum)
	}
	return fmt.Sprintf("%d (%d unavailable)", sum, unavailable)
}

func printStoryRounds(cmd *cobra.Command, rep costview.ReviewRoundsReport) error {
	out := cmd.OutOrStdout()
	for i, e := range rep.Edges {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s %s→%s: %d presentation(s) — %d accepted, %d rejected, %d no verdict — %s\n",
			rep.StoryID, e.From, e.To, e.Presentations, e.Accepted, e.Rejected, e.NoVerdict, edgeState(e))
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ROUND\tATTEMPT\tRESULT\tSEAT\tSKILL\tTURNS\tTOOL CALLS\tOUTCOME\tDURATION\tTOKENS IN/OUT")
		for _, r := range e.Rounds {
			// The performer that re-presented the round sits beside its gate seats; its
			// outcome cell says how many of the presented findings it answered.
			if p := r.Performer; p != nil {
				fmt.Fprintf(w, "%d\t%s\t%s\tperformer %s\t%s\t%s\t\tfindings %d answered %d (adopted %d, rebutted %d)\t%s\t%d/%d\n",
					r.Round, r.Attempt, r.Result, p.Agent, p.Skill, countFigure(p.Turns, p.TurnsUnavailableReason),
					p.FindingsPresented, p.Answered, p.Adopted, p.Rebutted, fmtDurationMs(p.DurationMs), p.TokensIn, p.TokensOut)
			}
			if len(r.Seats) == 0 && r.Performer == nil {
				fmt.Fprintf(w, "%d\t%s\t%s\t\t\t\t\t\t\t\n", r.Round, r.Attempt, r.Result)
				continue
			}
			for _, s := range r.Seats {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n",
					r.Round, r.Attempt, r.Result, s.Seat, s.Skill,
					countFigure(s.Turns, s.TurnsUnavailableReason), countFigure(s.ToolCalls, s.ToolCallsUnavailableReason),
					s.Outcome, fmtDurationMs(s.DurationMs))
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// edgeState renders where an edge's rounds ended: the round it converged or
// stopped at, or that it is still open.
func edgeState(e costview.EdgeReviewRounds) string {
	switch e.State {
	case costview.EdgeConverged:
		return fmt.Sprintf("converged at round %d", e.ConvergedAt)
	case costview.EdgeStopped:
		return fmt.Sprintf("stopped at round %d", e.StoppedAt)
	}
	return "open"
}

// countFigure renders a measured count, or "—" with the adapter-named reason it
// is unavailable.
func countFigure(n *int, reason string) string {
	if n != nil {
		return fmt.Sprint(*n)
	}
	if reason == "" {
		return "—"
	}
	return "— (" + reason + ")"
}
