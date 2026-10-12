package ctxaudit

import (
	"strings"
	"testing"
)

const namingPassage = "A substrate artifact name encodes its type so owner and kind read from the name alone: " +
	"every reviewer follows object function and a bare object review is that object's structure reviewer."

func onDemandNaming() Doc {
	return Doc{
		Kind: "principles", Name: "satelle-skill-naming", Residency: "ondemand",
		Where: ".satelle/principles/satelle-skill-naming.md",
		Body:  "---\nname: satelle-skill-naming\n---\n# Naming\n\n" + namingPassage + "\n",
	}
}

func constitutionWith(body string) Doc {
	return Doc{Kind: "constitution", Name: "constitution", Where: ".satelle/constitution.md", Body: "# Constitution\n\n" + body + "\n"}
}

// A constitution section that copies a passage of an on-demand principle the
// context never receives is found: the comparison is against the whole corpus.
func TestDuplicatesFindsCopyOfUnlinkedOnDemandPrinciple(t *testing.T) {
	cons := constitutionWith("## Substrate naming\n\n" + namingPassage)
	ctx := Context{Label: "session[claude]", Parts: []Doc{cons}}
	got := Duplicates([]Context{ctx}, []Doc{cons, onDemandNaming()}, 10)
	if len(got) != 1 {
		t.Fatalf("want one duplicate, got %d: %+v", len(got), got)
	}
	d := got[0]
	if d.A.Name != "constitution" || d.B.Name != "satelle-skill-naming" {
		t.Errorf("must name both documents, got %q and %q", d.A.Where, d.B.Where)
	}
	if !strings.Contains(d.Quote, "encodes its type so owner and kind read from the name alone") {
		t.Errorf("must quote the overlapping passage, got %q", d.Quote)
	}
	if d.Context != "session[claude]" || d.Words < 10 {
		t.Errorf("context/words: %+v", d)
	}
}

// Referring to the principle by [[name]] is the cure, not a duplicate.
func TestDuplicatesIgnoresReferenceByLink(t *testing.T) {
	cons := constitutionWith("## Substrate naming\n\nA name encodes the artifact's type; see [[satelle-skill-naming]].")
	ctx := Context{Label: "session[claude]", Parts: []Doc{cons}}
	if got := Duplicates([]Context{ctx}, []Doc{cons, onDemandNaming()}, 10); len(got) != 0 {
		t.Fatalf("a reference by [[name]] must not be reported: %+v", got)
	}
}

func TestDuplicatesThresholdAndDegenerateInputs(t *testing.T) {
	cons := constitutionWith(namingPassage)
	ctx := Context{Label: "c", Parts: []Doc{cons}}
	corpus := []Doc{cons, onDemandNaming()}
	if got := Duplicates([]Context{ctx}, corpus, 0); got != nil {
		t.Errorf("minWords 0 must report nothing, got %+v", got)
	}
	if got := Duplicates([]Context{ctx}, corpus, 500); len(got) != 0 {
		t.Errorf("a run shorter than the threshold must not be reported: %+v", got)
	}
	// A part is never compared with its own source.
	if got := Duplicates([]Context{ctx}, []Doc{cons}, 5); len(got) != 0 {
		t.Errorf("a document is not a duplicate of itself: %+v", got)
	}
}

// Parts of one context are compared with each other even when the corpus does
// not carry them.
func TestDuplicatesComparesPartsOfOneContext(t *testing.T) {
	a := Doc{Kind: "skills", Name: "one", Where: "skills/one.md", Body: "# One\n\n" + namingPassage}
	b := Doc{Kind: "skills", Name: "two", Where: "skills/two.md", Body: "# Two\n\n" + namingPassage}
	got := Duplicates([]Context{{Label: "seat[x]", Parts: []Doc{a, b}}}, nil, 10)
	if len(got) != 1 {
		t.Fatalf("want the sibling pair, got %+v", got)
	}
}

// A passage copied into several documents is one defect, reported once.
func TestGroupDuplicatesFoldsPairsIntoOnePassage(t *testing.T) {
	docs := make([]Doc, 4)
	for i, n := range []string{"a", "b", "c", "d"} {
		docs[i] = Doc{Kind: "skills", Name: n, Where: "skills/" + n + ".md", Body: "# " + n + "\n\n" + namingPassage + " unique tail " + n}
	}
	ds := Duplicates([]Context{{Label: "seat[x]", Parts: docs}}, nil, 10)
	if len(ds) != 6 {
		t.Fatalf("4 copies are 6 pairs, got %d", len(ds))
	}
	groups := GroupDuplicates(ds, 10)
	if len(groups) != 1 || len(groups[0].Docs) != 4 {
		t.Fatalf("want one group of 4 documents, got %+v", groups)
	}
	if groups[0].Docs[0].Where != "skills/a.md" || groups[0].Docs[3].Where != "skills/d.md" {
		t.Errorf("documents must be sorted by where: %+v", groups[0].Docs)
	}
}

func TestStripFrontmatter(t *testing.T) {
	if got := StripFrontmatter("---\nname: x\n---\nbody"); got != "body" {
		t.Errorf("got %q", got)
	}
	if got := StripFrontmatter("no frontmatter"); got != "no frontmatter" {
		t.Errorf("got %q", got)
	}
}
