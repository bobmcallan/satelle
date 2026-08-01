package agentvalidate

import "github.com/bobmcallan/satelle/internal/docindex"

// A lifecycle is a DERIVED ROUTE — done.md + step.md — and there is no DOT front
// end left to author one with (sty_d953c5d8). routeDocs builds the two halves a
// fixture needs, so the allocation checks see the same nodes, gates and bindings
// the retired graphs declared.
func routeDocs(done, step string) []docindex.Doc {
	return []docindex.Doc{
		{Kind: "workflows", Name: "done",
			Body: "---\nname: done\ntype: workflow\nscope: system\ndescription: fixture done\n---\n\n" + done},
		{Kind: "workflows", Name: "step",
			Body: "---\nname: step\ntype: workflow\nscope: system\ndescription: fixture steps\n---\n\n" + step},
	}
}
