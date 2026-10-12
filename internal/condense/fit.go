package condense

import (
	"strings"

	"github.com/bobmcallan/satelle/internal/compact"
	"github.com/bobmcallan/satelle/internal/retrieve"
)

// Item is one resident document offered to Fit.
type Item struct {
	Name string // identity in the Rung report and in the retrieve marker
	// Group is a heading rendered once, before the first inline item that carries
	// it. Heading is a line rendered before this item's body. Either may be "".
	Group   string
	Heading string
	Body    string // the text delivered at the full rung
	// Original is the exact authored text a retrieve marker returns; "" means
	// Body is the original.
	Original string
	// Pointer is the one-line stand-in used at the pointer rung. "" means this
	// item has none and is never pointered.
	Pointer string
}

// Frame is the fixed text around the items.
type Frame struct {
	PointerHeader string // rendered before the pointer lines, when there are any
	Trailer       string // rendered last, always
}

// Rung reports which rung one item was delivered at.
type Rung struct {
	Name string // the item
	Rung string // full, filler, lead or pointer
}

// Pointered reports whether any item was reduced to a pointer.
func Pointered(rungs []Rung) bool {
	for _, r := range rungs {
		if r.Rung == RungPointer {
			return true
		}
	}
	return false
}

// form is one way an item can be delivered.
type form struct {
	rung    string
	body    string // the text, without its marker
	pointer bool
}

// Fit renders items and frame within budget, as measured by measure, and says
// which rung each item was delivered at. It is the only place the ladder is
// applied: every body starts at the full rung. While the render is over budget
// one body takes one step down (full → filler → lead): the step that closes the
// gap while losing the least, else the one that saves the most. Only when no body
// can step down does a body fall to its pointer, chosen the same way. Once the
// render fits, bodies are stepped back up wherever the budget allows, so no more
// is condensed than the budget requires. Everything below the full rung carries a
// marker whose stored bytes are the item's original, put through off (nil sizes
// the marker without storing anything — the render is then an estimate, not a
// deliverable).
//
// A budget of zero or less leaves the ladder off: every body is delivered as
// written. The result is deterministic for the same inputs.
func Fit(items []Item, frame Frame, budget int, measure func(string) int, rules Rules, off compact.Offloader) (string, []Rung, error) {
	rules, err := rules.ready()
	if err != nil {
		return "", nil, err
	}
	forms := make([][]form, len(items))
	for i, it := range items {
		forms[i] = formsFor(it, rules, measure)
	}
	at := make([]int, len(items))
	hashes := map[int]string{}
	render := func() string { return renderItems(items, forms, at, hashes, frame) }

	if budget > 0 {
		for measure(render()) > budget {
			i := nextToStep(items, forms, at, hashes, frame, budget, measure)
			if i < 0 {
				break
			}
			at[i]++
		}
		if measure(render()) <= budget {
			stepBackUp(at, budget, measure, render)
		}
	}
	for i, it := range items {
		if f := forms[i][at[i]]; at[i] > 0 && !f.pointer && off != nil {
			hash, perr := off.Put([]byte(original(it)))
			if perr != nil {
				return "", nil, perr
			}
			hashes[i] = hash
		}
	}
	out := render()
	if budget > 0 && measure(out) > budget {
		if lines := pointerLines(items, forms, at); len(lines) > 0 {
			out = composeWithin(frame.PointerHeader, lines, frame.Trailer, budget, measure)
		}
	}
	rungs := make([]Rung, len(items))
	for i, it := range items {
		rungs[i] = Rung{Name: it.Name, Rung: forms[i][at[i]].rung}
	}
	return out, rungs, nil
}

func original(it Item) string {
	if it.Original != "" {
		return it.Original
	}
	return it.Body
}

// formsFor lists the ways an item can be delivered, full first. A rung that
// leaves the text unchanged, or does not make the delivered text smaller once
// its marker is counted, is left out.
func formsFor(it Item, rules Rules, measure func(string) int) []form {
	forms := []form{{rung: RungFull, body: it.Body}}
	// Each rung works on what the rung before it delivered, so the filler a rung
	// dropped stays dropped further down the ladder.
	cur := it.Body
	size := measure(it.Body)
	for _, name := range rules.Ladder {
		var next string
		switch name {
		case RungFiller:
			next = dropFiller(cur, rules)
		case RungLead:
			next = lead(cur, rules)
		default:
			continue
		}
		if next == cur {
			continue
		}
		cur = next
		f := form{rung: name, body: next}
		if n := measure(withMarker(it, f, retrieve.Hash([]byte(original(it))))); n < size {
			forms = append(forms, f)
			size = n
		}
	}
	if rules.hasRung(RungPointer) && it.Pointer != "" {
		forms = append(forms, form{rung: RungPointer, body: it.Pointer, pointer: true})
	}
	return forms
}

// withMarker is the text delivered for f: the condensed body and the line that
// names where the original is kept. The full rung and a pointer carry none.
func withMarker(it Item, f form, hash string) string {
	if f.rung == RungFull || f.pointer {
		return f.body
	}
	return f.body + "\n\n" + retrieveNote + hash + "]"
}

// retrieveNote opens the marker line; the hash and "]" follow. It is the tail of
// retrieve.Summary's grammar, so retrieve.MarkerRE and retrieve.FindHashes read it.
const retrieveNote = "[Retrieve more: satelle retrieve "

func renderItems(items []Item, forms [][]form, at []int, hashes map[int]string, frame Frame) string {
	var b strings.Builder
	seen := map[string]bool{}
	for i, it := range items {
		f := forms[i][at[i]]
		if f.pointer {
			continue
		}
		if it.Group != "" && !seen[it.Group] {
			seen[it.Group] = true
			b.WriteString(it.Group + "\n\n")
		}
		if it.Heading != "" {
			b.WriteString(it.Heading + "\n\n")
		}
		hash, ok := hashes[i]
		if !ok {
			hash = retrieve.Hash([]byte(original(it)))
		}
		b.WriteString(withMarker(it, f, hash) + "\n\n")
	}
	if lines := pointerLines(items, forms, at); len(lines) > 0 {
		b.WriteString(frame.PointerHeader + "\n" + strings.Join(lines, "\n") + "\n\n")
	}
	b.WriteString(frame.Trailer)
	return b.String()
}

func pointerLines(items []Item, forms [][]form, at []int) []string {
	var lines []string
	for i := range items {
		if f := forms[i][at[i]]; f.pointer {
			lines = append(lines, f.body)
		}
	}
	return lines
}

// nextToStep picks the item that takes the next step down, trying every
// condensed step before any pointer step. Each candidate is rendered: of those
// that bring the whole within budget the one that leaves it largest (the least
// lost) wins, and when none does, the one that leaves it smallest (the most
// saved). Ties go to the earlier item. -1 when nothing can step.
func nextToStep(items []Item, forms [][]form, at []int, hashes map[int]string, frame Frame, budget int, measure func(string) int) int {
	for _, toPointer := range []bool{false, true} {
		best, bestSize, bestFits := -1, 0, false
		for i := range items {
			if at[i]+1 >= len(forms[i]) || forms[i][at[i]+1].pointer != toPointer {
				continue
			}
			at[i]++
			size := measure(renderItems(items, forms, at, hashes, frame))
			at[i]--
			fits := size <= budget
			better := best < 0 || (fits && !bestFits) ||
				(fits && bestFits && size > bestSize) || (!fits && !bestFits && size < bestSize)
			if better {
				best, bestSize, bestFits = i, size, fits
			}
		}
		if best >= 0 {
			return best
		}
	}
	return -1
}

// stepBackUp raises bodies one rung at a time, in order, wherever the render
// still fits, until none can rise: a gap closed by a large step leaves room that
// the bodies stepped down before it can take back.
func stepBackUp(at []int, budget int, measure func(string) int, render func() string) {
	for changed := true; changed; {
		changed = false
		for i := range at {
			if at[i] == 0 {
				continue
			}
			at[i]--
			if measure(render()) <= budget {
				changed = true
				continue
			}
			at[i]++
		}
	}
}

// composeWithin keeps the pointer header and as many pointer lines as fit, then
// the trailer if room remains. It never slices a line.
func composeWithin(header string, lines []string, trailer string, budget int, measure func(string) int) string {
	var out []string
	used := 0
	add := func(line string) bool {
		n := measure(line) + 1
		if used+n > budget {
			return false
		}
		out = append(out, line)
		used += n
		return true
	}
	if header != "" {
		add(header)
	}
	for _, line := range lines {
		if !add(line) {
			break
		}
	}
	if trailer != "" && used+measure(trailer)+1 <= budget {
		out = append(out, trailer)
	}
	return strings.Join(out, "\n")
}
