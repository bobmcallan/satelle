package condense

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/retrieve"
)

func embeddedRules(t *testing.T) Rules {
	t.Helper()
	r, err := ParseRules(config.EmbeddedCondense())
	if err != nil {
		t.Fatalf("embedded condense rules: %v", err)
	}
	return r
}

func bytesOf(s string) int { return len(s) }

// store is the in-memory offloader the markers are resolved against.
type store map[string][]byte

func (s store) Put(content []byte) (string, error) {
	h := retrieve.Hash(content)
	s[h] = append([]byte(nil), content...)
	return h, nil
}

// prose is a paragraph of n sentences; the second and third hold a negation, so
// the lead rung keeps them, and the rest are plain.
func prose(tag string, paragraphs int) string {
	var ps []string
	for i := 0; i < paragraphs; i++ {
		ps = append(ps, fmt.Sprintf(
			"%s opens paragraph %d with a plain statement of intent. Never skip the %s step of paragraph %d. "+
				"Do not reorder step %d of %s. This closing sentence is plain commentary that adds nothing a reader needs.",
			tag, i, tag, i, i, tag))
	}
	return strings.Join(ps, "\n\n")
}

func items(sizes ...int) []Item {
	var out []Item
	for i, n := range sizes {
		name := fmt.Sprintf("doc-%d", i)
		body := prose(name, n)
		out = append(out, Item{
			Name: name, Group: "# Resident", Heading: "### " + name, Body: body,
			Original: "---\nname: " + name + "\n---\n" + body,
			Pointer:  "- `" + name + "` — read: `satelle doc get principles " + name + "`",
		})
	}
	return out
}

func rungOf(rungs []Rung, name string) string {
	for _, r := range rungs {
		if r.Name == name {
			return r.Rung
		}
	}
	return ""
}

func TestFitUnbudgetedDeliversEveryBodyAsWritten(t *testing.T) {
	its := items(3, 3)
	out, rungs, err := Fit(its, Frame{Trailer: "END"}, 0, bytesOf, embeddedRules(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range its {
		if !strings.Contains(out, it.Body) {
			t.Errorf("%s not delivered as written", it.Name)
		}
	}
	for _, r := range rungs {
		if r.Rung != RungFull {
			t.Errorf("%s at %s, want full", r.Name, r.Rung)
		}
	}
	if strings.Contains(out, "Retrieve more") {
		t.Errorf("a full body carries no marker:\n%s", out)
	}
}

func TestFitCondensesBeforeAnyPointerAndTheLargestFirst(t *testing.T) {
	its := items(8, 2, 1)
	full, _, _ := Fit(its, Frame{}, 0, bytesOf, embeddedRules(t), nil)
	// A budget that the largest body alone, condensed, can satisfy.
	budget := len(full) - 400
	st := store{}
	out, rungs, err := Fit(its, Frame{}, budget, bytesOf, embeddedRules(t), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > budget {
		t.Fatalf("%d bytes over a budget of %d", len(out), budget)
	}
	if got := rungOf(rungs, "doc-0"); got != RungLead {
		t.Errorf("the largest body must step down first, got %q", got)
	}
	for _, r := range rungs {
		if r.Rung == RungPointer {
			t.Errorf("%s fell to a pointer while a condensed body would fit", r.Name)
		}
	}
	if got := rungOf(rungs, "doc-2"); got != RungFull {
		t.Errorf("a body that need not shrink stays full, got %q", got)
	}
}

func TestFitTakesTheSmallestStepThatClosesTheGap(t *testing.T) {
	its := items(8, 3, 1)
	full, _, _ := Fit(its, Frame{}, 0, bytesOf, embeddedRules(t), nil)
	// A gap the middle body's lead closes on its own: the largest body, which
	// would save far more, stays as written.
	out, rungs, err := Fit(its, Frame{}, len(full)-50, bytesOf, embeddedRules(t), store{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > len(full)-50 {
		t.Fatalf("%d bytes over the budget", len(out)-(len(full)-50))
	}
	if got := rungOf(rungs, "doc-0"); got != RungFull {
		t.Errorf("the largest body is condensed for a small gap: %v", rungs)
	}
	if got := rungOf(rungs, "doc-1"); got != RungLead {
		t.Errorf("the body whose step closes the gap is condensed, got %v", rungs)
	}
}

// A gap no condensing closes costs one pointer; what that pointer frees beyond
// the gap is spent on stepping the other bodies back up.
func TestFitSpendsWhatAPointerFreesOnTheOtherBodies(t *testing.T) {
	its := items(8, 6, 6)
	frame := Frame{PointerHeader: "OMITTED"}
	bare := make([]Item, len(its))
	copy(bare, its)
	for i := range bare {
		bare[i].Pointer = ""
	}
	floor, _, _ := Fit(bare, frame, 1, bytesOf, embeddedRules(t), nil) // every body at its lead
	budget := len(floor) - 100
	out, rungs, err := Fit(its, frame, budget, bytesOf, embeddedRules(t), store{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > budget {
		t.Fatalf("%d bytes over a budget of %d", len(out), budget)
	}
	pointered, full := 0, 0
	for _, r := range rungs {
		switch r.Rung {
		case RungPointer:
			pointered++
		case RungFull:
			full++
		}
	}
	if pointered != 1 || full == 0 {
		t.Errorf("want one pointer and the freed room spent on full bodies, got %v", rungs)
	}
}

func TestFitMarkersReturnTheAuthoredBytes(t *testing.T) {
	its := items(8, 6, 4)
	st := store{}
	out, rungs, err := Fit(its, Frame{}, 2500, bytesOf, embeddedRules(t), st)
	if err != nil {
		t.Fatal(err)
	}
	hashes := retrieve.FindHashes(out)
	condensed := 0
	for i, r := range rungs {
		if r.Rung != RungFull && r.Rung != RungPointer {
			condensed++
			if got := st[retrieve.Hash([]byte(its[i].Original))]; string(got) != its[i].Original {
				t.Errorf("%s: stored original differs from the authored text", r.Name)
			}
		}
	}
	if condensed == 0 || len(hashes) != condensed {
		t.Fatalf("want one marker per condensed body (%d), got %d in:\n%s", condensed, len(hashes), out)
	}
	for _, h := range hashes {
		if _, ok := st[h]; !ok {
			t.Errorf("marker %s resolves to nothing", h)
		}
	}
}

func TestFitNilOffloaderSizesTheSameRender(t *testing.T) {
	its := items(8, 6, 4)
	a, _, _ := Fit(its, Frame{}, 2500, bytesOf, embeddedRules(t), store{})
	b, _, _ := Fit(its, Frame{}, 2500, bytesOf, embeddedRules(t), nil)
	if a != b {
		t.Errorf("a dry fit must size exactly what a stored fit delivers")
	}
}

func TestFitFallsToPointersOnlyWhenTheLadderIsExhausted(t *testing.T) {
	its := items(8, 6, 4)
	out, rungs, err := Fit(its, Frame{PointerHeader: "OMITTED", Trailer: "END"}, 900, bytesOf, embeddedRules(t), store{})
	if err != nil {
		t.Fatal(err)
	}
	if !Pointered(rungs) {
		t.Fatalf("a budget no condensed body can meet must use pointers:\n%s", out)
	}
	if len(out) > 900 {
		t.Errorf("%d bytes over the budget of 900", len(out))
	}
	// Every body that is still inline sits at its last condensed rung.
	for _, r := range rungs {
		if r.Rung == RungFull {
			t.Errorf("%s still full while another fell to a pointer", r.Name)
		}
	}
}

func TestFitIsDeterministic(t *testing.T) {
	its := items(8, 6, 4)
	a, ra, _ := Fit(its, Frame{Trailer: "END"}, 3000, bytesOf, embeddedRules(t), store{})
	b, rb, _ := Fit(its, Frame{Trailer: "END"}, 3000, bytesOf, embeddedRules(t), store{})
	if a != b || fmt.Sprint(ra) != fmt.Sprint(rb) {
		t.Error("the same inputs must give the same render and rungs")
	}
}

func TestFitMeasuresInTheCallersUnit(t *testing.T) {
	its := items(8)
	runes := func(s string) int { return len([]rune(s)) }
	out, _, _ := Fit(its, Frame{}, 1500, runes, embeddedRules(t), store{})
	if runes(out) > 1500 {
		t.Errorf("%d runes over a rune budget of 1500", runes(out))
	}
}

func TestLeadKeepsStructureFirstSentencesAndProtectedClauses(t *testing.T) {
	r := embeddedRules(t)
	body := "# Title\n\nFirst sentence of the paragraph. A plain second sentence. Never touch `x.go`; keep calm. A plain closing sentence.\n\n" +
		"```\ncode stays. exactly: as written\n```\n\n- Item one. Plain tail.\n- Item two never changes. Plain tail."
	got := lead(body, r)
	for _, want := range []string{
		"# Title", "First sentence of the paragraph.", "Never touch `x.go`;",
		"code stays. exactly: as written", "- Item one.", "- Item two never changes.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lead dropped %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"A plain second sentence", "A plain closing sentence", "Plain tail", "keep calm"} {
		if strings.Contains(got, gone) {
			t.Errorf("lead kept %q:\n%s", gone, got)
		}
	}
}

func TestLeadDoesNotEndASentenceAtAnAbbreviation(t *testing.T) {
	got := lead("- What lives in the binary vs. the substrate, and why. Plain tail.", embeddedRules(t))
	if !strings.Contains(got, "vs. the substrate, and why.") {
		t.Errorf("an abbreviation cut the sentence: %q", got)
	}
}

func TestLeadNeverSplitsInsideInlineCode(t *testing.T) {
	got := lead("Run `satelle story set <id> --status plan. now` first. Plain tail.", embeddedRules(t))
	if !strings.Contains(got, "`satelle story set <id> --status plan. now` first.") {
		t.Errorf("inline code was cut: %q", got)
	}
}

func TestFillerDropsOnlyOutsideProtectedSpans(t *testing.T) {
	r := embeddedRules(t)
	got := dropFiller("This is really just a plain line.\nUse `just run` here, never just once.\n```\njust code\n```", r)
	if strings.Contains(strings.Split(got, "\n")[0], "really") || strings.Contains(strings.Split(got, "\n")[0], "just") {
		t.Errorf("filler survived in prose: %q", got)
	}
	for _, keep := range []string{"`just run`", "never once", "just code"} {
		if !strings.Contains(got, keep) {
			t.Errorf("filler removed from a protected span, lost %q:\n%s", keep, got)
		}
	}
}

func TestEveryRungSkipsWhenItWouldNotShrinkTheBody(t *testing.T) {
	it := Item{Name: "tiny", Body: "One short sentence.", Pointer: "- `tiny`"}
	forms := formsFor(it, embeddedRules(t), bytesOf)
	if len(forms) != 2 || forms[0].rung != RungFull || !forms[1].pointer {
		t.Errorf("a body with nothing to condense has only full and pointer forms, got %+v", forms)
	}
}
