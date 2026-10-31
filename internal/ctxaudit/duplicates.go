// duplicates.go — verbatim duplicated guidance between two documents.
//
// The constitution's rule is a simple project context plus REFERENCES to
// principles and skills, not restatements of them. This finds the restatement
// that is word-for-word: a run of at least minWords consecutive words (case,
// whitespace and markdown punctuation ignored) shared by two documents. It is
// deterministic — no model, no network. Paraphrase is the opt-in semantic
// review, not this.
//
// The threshold is the caller's: it is configuration (config.DoctorConfig), not
// a number in this package.
package ctxaudit

import (
	"hash/fnv"
	"sort"
	"strings"
	"unicode"
)

// Doc is one document of the substrate, or one part of a context an agent
// receives. Kind and Name together identify it; Where is how a finding names it
// (the file the operator would edit).
type Doc struct {
	Kind      string // constitution | principles | skills
	Name      string
	Residency string // session | seat | ondemand ("" for a non-principle)
	Where     string
	Body      string
}

// Source is the stable identity of the document: its kind and name.
func (d Doc) Source() string { return d.Kind + "/" + d.Name }

// Context is one assembled context an agent receives, as labelled parts.
type Context struct {
	Label string // session[claude], seat[coder]
	Parts []Doc
}

// Duplicate is one passage shared word-for-word by two documents.
type Duplicate struct {
	Context string // the first context in which the pair was seen
	A, B    Doc    // A is the context part, B the document it overlaps
	Quote   string // the shared run, normalised
	Words   int
}

// shingleWords turns a body into the comparison alphabet: frontmatter dropped,
// lower-cased, every run of non-letter/digit characters a single separator.
func shingleWords(body string) []string {
	return strings.FieldsFunc(strings.ToLower(StripFrontmatter(body)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// StripFrontmatter drops a leading YAML frontmatter block, if any.
func StripFrontmatter(body string) string {
	if !strings.HasPrefix(body, "---\n") {
		return body
	}
	rest := body[4:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		after := rest[i+4:]
		if j := strings.Index(after, "\n"); j >= 0 {
			return after[j+1:]
		}
		return ""
	}
	return body
}

func shingleKey(w []string) uint64 {
	h := fnv.New64a()
	for _, s := range w {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

type shingleLoc struct{ doc, pos int }

// Duplicates reports, for every part of every context, each other document
// (from corpus, or another part of the same context) that shares a run of at
// least minWords words with it. A part is never compared with its own source.
// Each unordered pair of documents is reported once, with its longest shared
// run, under the first context it was seen in. minWords < 1 reports nothing.
func Duplicates(contexts []Context, corpus []Doc, minWords int) []Duplicate {
	if minWords < 1 {
		return nil
	}
	// The comparison set: the corpus, plus any context part the corpus does not
	// carry (so a part is still compared with its siblings).
	all := append([]Doc(nil), corpus...)
	have := map[string]bool{}
	for _, d := range all {
		have[d.Source()] = true
	}
	for _, c := range contexts {
		for _, p := range c.Parts {
			if !have[p.Source()] {
				have[p.Source()] = true
				all = append(all, p)
			}
		}
	}
	words := make([][]string, len(all))
	index := map[uint64][]shingleLoc{}
	bySource := map[string]int{}
	for i, d := range all {
		bySource[d.Source()] = i
		words[i] = shingleWords(d.Body)
		for p := 0; p+minWords <= len(words[i]); p++ {
			k := shingleKey(words[i][p : p+minWords])
			index[k] = append(index[k], shingleLoc{i, p})
		}
	}

	best := map[string]Duplicate{}
	var order []string
	for _, c := range contexts {
		for _, part := range c.Parts {
			ai := bySource[part.Source()]
			pw := words[ai]
			for i := 0; i+minWords <= len(pw); {
				advance := 1
				for _, loc := range index[shingleKey(pw[i:i+minWords])] {
					if loc.doc == ai || all[loc.doc].Source() == part.Source() {
						continue
					}
					cw := words[loc.doc]
					n := 0
					for i+n < len(pw) && loc.pos+n < len(cw) && pw[i+n] == cw[loc.pos+n] {
						n++
					}
					if n < minWords { // a hash collision, not a shared run
						continue
					}
					a, b := part.Source(), all[loc.doc].Source()
					if b < a {
						a, b = b, a
					}
					key := a + "\x00" + b
					prev, seen := best[key]
					if !seen {
						order = append(order, key)
					}
					if !seen || n > prev.Words {
						best[key] = Duplicate{Context: c.Label, A: part, B: all[loc.doc], Quote: quote(pw[i : i+n]), Words: n}
					}
					if n > advance {
						advance = n
					}
				}
				i += advance
			}
		}
	}
	sort.Strings(order)
	out := make([]Duplicate, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

// DuplicateGroup is every document that carries one shared passage. A passage
// copied into N documents is N·(N-1)/2 pairs but one defect, so it is reported
// once with all of its documents.
type DuplicateGroup struct {
	Docs    []Doc  // sorted by Where
	Context string // the first context the passage was seen in
	Quote   string // the longest run
	Words   int    // its length
}

// GroupDuplicates folds pairwise duplicates that start at the same passage — the
// first minWords words of their shared run — into one group each.
func GroupDuplicates(ds []Duplicate, minWords int) []DuplicateGroup {
	type acc struct {
		g    DuplicateGroup
		seen map[string]bool
	}
	groups := map[string]*acc{}
	var order []string
	for _, d := range ds {
		fields := strings.Fields(d.Quote)
		if len(fields) > minWords {
			fields = fields[:minWords]
		}
		sig := strings.Join(fields, " ")
		a, ok := groups[sig]
		if !ok {
			a = &acc{g: DuplicateGroup{Context: d.Context}, seen: map[string]bool{}}
			groups[sig] = a
			order = append(order, sig)
		}
		for _, doc := range []Doc{d.A, d.B} {
			if !a.seen[doc.Source()] {
				a.seen[doc.Source()] = true
				a.g.Docs = append(a.g.Docs, doc)
			}
		}
		if d.Words > a.g.Words {
			a.g.Words, a.g.Quote = d.Words, d.Quote
		}
	}
	out := make([]DuplicateGroup, 0, len(order))
	for _, sig := range order {
		g := groups[sig].g
		sort.Slice(g.Docs, func(i, j int) bool { return g.Docs[i].Where < g.Docs[j].Where })
		out = append(out, g)
	}
	return out
}

// quote renders a shared run for a finding, bounded so one long copy cannot
// swamp the report.
func quote(w []string) string {
	const max = 240
	s := strings.Join(w, " ")
	if len(s) > max {
		cut := s[:max]
		if i := strings.LastIndexByte(cut, ' '); i > 0 {
			cut = cut[:i]
		}
		s = cut + " …"
	}
	return s
}
