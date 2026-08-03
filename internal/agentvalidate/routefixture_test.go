package agentvalidate

import "github.com/bobmcallan/satelle/internal/docindex"

// A lifecycle is a DERIVED ROUTE — done.toml + step.toml — and there is no DOT
// front end left to author one with (sty_d953c5d8). routeDocs builds the two
// halves a fixture needs, so the allocation checks see the same nodes, gates and
// bindings the retired graphs declared.
//
// Both halves are TOML with a `[meta]` header rather than a `---` block
// (sty_81bb0dde): a YAML envelope around a TOML body is not a document either
// parser can read, and the halves would resolve to an empty route — which reads
// as "nothing is allocated" rather than as a broken fixture.
func routeDocs(done, step string) []docindex.Doc {
	head := func(name, what string) string {
		return "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"system\"\ndescription = \"" +
			what + "\"\n\n"
	}
	return []docindex.Doc{
		{Kind: "workflows", Name: "done", Body: head("done", "fixture done") + done},
		{Kind: "workflows", Name: "step", Body: head("step", "fixture steps") + step},
	}
}
