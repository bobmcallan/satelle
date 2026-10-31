package agentstep

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/condense"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/retrieve"
)

// seatPrinciple is a session-resident principle of about n paragraphs. The
// second and third sentence of each hold a negation, so the lead rung keeps
// them; the closing sentence is plain commentary the lead rung drops.
func seatPrinciple(name string, n int) docindex.Doc {
	var ps []string
	for i := 0; i < n; i++ {
		ps = append(ps, fmt.Sprintf(
			"%s states paragraph %d plainly. Never skip step %d of %s. Do not reorder the %s steps. "+
				"This closing sentence is plain commentary that adds nothing a reader needs.", name, i, i, name, name))
	}
	body := "---\nname: " + name + "\ntags: [principles:session]\n---\n# " + name + "\n\n" + strings.Join(ps, "\n\n") + "\n"
	return docindex.Doc{Kind: "principles", Name: name, Body: body}
}

func seatEngine(t *testing.T, bytesBudget int) (*Engine, map[string][]byte, []docindex.Doc) {
	t.Helper()
	principles := []docindex.Doc{
		seatPrinciple(config.OperatingPrinciple, 10), seatPrinciple("p-two", 8),
		seatPrinciple("p-three", 6), seatPrinciple("p-four", 4),
	}
	g := New(&fakeRunner{}, fakeDocs{workflow: testWorkflow, extraPrinciples: principles}, "/repo", "")
	g.SetConstitution("A short constitution. Never edit without a story.")
	stored := map[string][]byte{}
	g.SetDiffOffloader(func(_ context.Context, _ string, content []byte) (string, error) {
		h := retrieve.Hash(content)
		stored[h] = append([]byte(nil), content...)
		return h, nil
	})
	if bytesBudget > 0 {
		g.SetInjected(config.InjectedConfig{BytesPerToken: 1, Budget: map[string]int{"reviewer": bytesBudget}}, condense.Rules{})
	}
	return g, stored, principles
}

func seatPrompt(t *testing.T, g *Engine, section string) string {
	t.Helper()
	req, err := g.buildRequest(context.Background(), invocation{
		charter: reviewerCharter(), rubric: "RUBRIC-BODY", principles: config.PrinciplesSession,
		payload: map[string]string{}, section: section, storyID: "sty_seat",
	})
	if err != nil {
		t.Fatal(err)
	}
	return req.SystemPrompt
}

func TestPayloadWithinBudgetKeepsEveryRule(t *testing.T) {
	full, _, principles := seatEngine(t, 0)
	unbudgeted := seatPrompt(t, full, "reviewer")
	for _, p := range principles {
		if !strings.Contains(unbudgeted, strings.TrimSpace(stripFrontmatter(p.Body))) {
			t.Fatalf("the unbudgeted arm must carry %s in full", p.Name)
		}
	}
	if strings.Contains(unbudgeted, "Retrieve more") {
		t.Fatal("the unbudgeted arm carries no marker")
	}

	budget := len(unbudgeted) - 1500
	g, stored, _ := seatEngine(t, budget)
	got := seatPrompt(t, g, "reviewer")
	if len(got) > budget {
		t.Fatalf("payload is %d bytes, over its budget of %d", len(got), budget)
	}
	if strings.Contains(got, "OMITTED FOR THE") {
		t.Fatalf("a payload the ladder can fit must not use pointers:\n%s", got)
	}
	// Every resident rule is delivered: in full, or condensed behind a marker
	// that returns the authored document.
	hashes := retrieve.FindHashes(got)
	if len(hashes) == 0 {
		t.Fatalf("a budget-bound payload carries retrieve markers:\n%s", got)
	}
	for _, h := range hashes {
		orig, ok := stored[h]
		if !ok {
			t.Fatalf("marker %s resolves to nothing", h)
		}
		found := false
		for _, p := range principles {
			found = found || string(orig) == p.Body
		}
		if !found {
			t.Errorf("marker %s does not return an authored principle", h)
		}
	}
	for _, p := range principles {
		inline := strings.Contains(got, strings.TrimSpace(stripFrontmatter(p.Body)))
		condensed := false
		for _, h := range hashes {
			condensed = condensed || string(stored[h]) == p.Body
		}
		if !inline && !condensed {
			t.Errorf("%s is neither in full nor behind a marker", p.Name)
		}
	}
	for _, tail := range []string{"RUBRIC-BODY", reviewerCharter()} {
		if !strings.Contains(got, tail) {
			t.Errorf("the charter and rubric are delivered as written, lost %q", tail[:20])
		}
	}
}

func TestPayloadOfASeatWithNoBudgetIsUnchanged(t *testing.T) {
	g, _, _ := seatEngine(t, 1500)
	// The planner has no budget in this fixture: ladder off, every body in full.
	a, b := seatPrompt(t, g, "planner"), seatPrompt(t, func() *Engine { e, _, _ := seatEngine(t, 0); return e }(), "planner")
	if a != b {
		t.Error("a seat without a budget must receive what it received before the ladder")
	}
}

// With nowhere to keep the originals nothing is condensed; a prompt left over
// the seat's budget is then refused by the dispatch backstop (sty_73a2dc68 AC3),
// never trimmed. Under a budget it fits, it is delivered in full.
func TestPayloadWithoutAStoreStaysInFull(t *testing.T) {
	g, _, _ := seatEngine(t, 1500)
	g.diffOffload = nil
	_, err := g.buildRequest(context.Background(), invocation{
		charter: reviewerCharter(), rubric: "RUBRIC-BODY", principles: config.PrinciplesSession,
		payload: map[string]string{}, section: "reviewer", storyID: "sty_seat",
	})
	if err == nil || !strings.Contains(err.Error(), `seat "reviewer"`) {
		t.Errorf("an uncondensable prompt over the seat's budget must be refused, got %v", err)
	}
	roomy, _, _ := seatEngine(t, 100000)
	roomy.diffOffload = nil
	if got := seatPrompt(t, roomy, "reviewer"); strings.Contains(got, "Retrieve more") || strings.Contains(got, "OMITTED") {
		t.Errorf("with nowhere to keep the originals nothing is condensed:\n%s", got)
	}
}

func TestSeatPayloadUsesFit(t *testing.T) {
	_, _, principles := seatEngine(t, 0)
	in := SeatPayloadInput{
		Constitution: "A short constitution.", Principles: principles, Charter: "CHARTER", Rubric: "RUBRIC",
		Section: "reviewer", BudgetBytes: 3500, Offload: offloadFunc(func(c []byte) (string, error) { return retrieve.Hash(c), nil }),
	}
	got, err := BuildSeatPayload(in)
	if err != nil {
		t.Fatal(err)
	}
	// The head of the prompt is exactly what condense.Fit returns for the same
	// items and the budget left after the fixed tail: BuildSeatPayload adds no
	// ladder of its own.
	tail := composeSystemPrompt(promptParts{charter: in.Charter, rubric: in.Rubric})
	items := []condense.Item{{Name: "constitution", Heading: "# Project constitution", Body: in.Constitution}}
	for _, p := range principles {
		items = append(items, condense.Item{
			Name: p.Name, Group: "# Always-resident principles (satelle)", Body: strings.TrimSpace(stripFrontmatter(p.Body)), Original: p.Body,
			Pointer: "- `" + p.Name + "` — read: `satelle doc get principles " + p.Name + "`",
		})
	}
	rules, err := condense.ParseRules(config.EmbeddedCondense())
	if err != nil {
		t.Fatal(err)
	}
	head, rungs, err := condense.Fit(items, condense.Frame{PointerHeader: "OMITTED FOR THE REVIEWER CONTEXT BUDGET (3500 bytes): the following are not inlined — read each now with the command shown, before doing any work."},
		3500-len(tail), func(s string) int { return len(s) }, rules, in.Offload)
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != head+tail || fmt.Sprint(got.Rungs) != fmt.Sprint(rungs) {
		t.Errorf("BuildSeatPayload must be condense.Fit plus the tail:\n got %q\nwant %q", got.SystemPrompt, head+tail)
	}
}

func TestSeatPayloadFallsToPointersOnlyPastTheLadder(t *testing.T) {
	_, _, principles := seatEngine(t, 0)
	got, err := SeatPayloadFor(SeatPromptInput{Kind: SeatReviewer, Judging: true, Section: "reviewer", Principles: principles, Rubric: "RUBRIC", BudgetBytes: 1300})
	if err != nil {
		t.Fatal(err)
	}
	if !condense.Pointered(got.Rungs) {
		t.Errorf("a budget no condensed body can meet falls to pointers, got %v", got.Rungs)
	}
	roomy, err := SeatPayloadFor(SeatPromptInput{Kind: SeatReviewer, Judging: true, Section: "reviewer", Principles: principles, Rubric: "RUBRIC", BudgetBytes: 6500})
	if err != nil {
		t.Fatal(err)
	}
	if condense.Pointered(roomy.Rungs) {
		t.Errorf("a budget the ladder can meet uses no pointer, got %v", roomy.Rungs)
	}
}
