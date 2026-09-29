package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/fixlane"
)

// fixLaneEdit is the edit gate's second look at a path edit the ordinary rule
// refused (sty_4b694872). It changes WHO MAY EDIT and nothing else: it never
// runs for a dispatched performer or a relay coder (a reviewer must never
// edit), it never runs when the ordinary rule already allowed the edit, and it
// grants only when a recorded, unconsumed, in-bound claim names exactly this
// path on the engaged story.
//
// allowed reports the grant. note is text appended to the ordinary deny reason
// so the agent learns why the lane did not open (empty when it has nothing to
// add). Every doubt — an unreadable ledger, an edit whose size cannot be
// measured — is a refusal: the lane fails closed.
func fixLaneEdit(info seatInfo, dm dispatchMarker, rm relayMarker, raw []byte, target string) (allowed bool, note string) {
	if dm != (dispatchMarker{}) || rm != (relayMarker{}) {
		return false, ""
	}
	if info.ItemID == "" || !info.Engaged || info.Stale || info.InFlight {
		return false, ""
	}
	a, err := app.Open()
	if err != nil {
		return false, ""
	}
	defer func() { _ = a.Close() }()
	ctx := context.Background()
	abs := resolveAbsTarget(a.RepoRoot, target)
	rel, inRepo := fixlane.RelPath(a.RepoRoot, abs)
	if !inRepo {
		return false, ""
	}
	claim, ok, err := fixlane.Live(ctx, a.Store.Ledger, info.ItemID, rel)
	if err != nil {
		return false, " — fix lane: cannot read the ledger (" + err.Error() + "), so no claim is honoured"
	}
	if !ok {
		if class, _ := fixlane.LastRefusal(ctx, a.Store.Ledger, info.ItemID, rel); class != "" {
			return false, fmt.Sprintf(" — fix lane: your claim on %s was REFUSED (class %s); the lane is closed for it", rel, class)
		}
		return false, ""
	}
	// The bound is re-applied at use, not only at claim: the configuration may
	// have tightened since the claim was recorded.
	if class, detail := fixlane.Classify(a.Config.ResolveFixLane(a.RepoRoot), fixlane.Input{
		StoryID: claim.StoryID, Path: claim.Path, Reason: claim.Reason,
		BoundLines: claim.BoundLines, ProvingTest: claim.ProvingTest,
	}, rel); class != "" {
		return false, fmt.Sprintf(" — fix lane: claim %s is no longer within the bound (class %s): %s", claim.ID, class, detail)
	}
	lines, measured := editLines(raw, abs)
	if !measured {
		return false, fmt.Sprintf(" — fix lane: claim %s cannot license this edit — its size cannot be measured, so the %d-line bound cannot be enforced", claim.ID, claim.BoundLines)
	}
	if lines > claim.BoundLines {
		return false, fmt.Sprintf(" — fix lane: this edit is %d lines, over claim %s's bound of %d", lines, claim.ID, claim.BoundLines)
	}
	// The use row is written BEFORE the edit is allowed, so the timeline reads
	// claim, use, change. The write is atomic per claim: a parallel edit racing
	// on the same claim loses and is refused. A failure to record is a refusal.
	if err := fixlane.Consume(ctx, a.Store.Ledger, claim, lines, time.Now()); err != nil {
		if errors.Is(err, fixlane.ErrConsumed) {
			return false, fmt.Sprintf(" — fix lane: claim %s was consumed by another edit", claim.ID)
		}
		return false, " — fix lane: cannot record the claim's use (" + err.Error() + "), so the edit is not allowed"
	}
	return true, ""
}

// editLines measures a PreToolUse edit event in changed lines — the size a
// claim's bound is held against. It reads no harness-specific shape: every JSON
// object inside the tool input counts as one edit, sized as the larger of the
// text it replaces and the text that replaces it (or the whole content of a
// write), and the edits sum.
//
// A replace-all edit rewrites EVERY occurrence, so its size is the per-
// occurrence size times the occurrences in the target file (target is the
// absolute path; it is read here) — measuring only the edit's own text would
// let a one-line pattern rewrite a whole file inside a small bound. Occurrences
// are also counted in the event's own new text, so a chained multi-edit cannot
// manufacture matches the file does not yet hold. When the file cannot be read
// the reach is unknowable.
//
// ok is false when the event carries no text to measure, or a replace-all whose
// reach cannot be established; the caller must not treat that edit as in-bound.
func editLines(raw []byte, target string) (lines int, ok bool) {
	var ev map[string]json.RawMessage
	if json.Unmarshal(raw, &ev) != nil {
		return 0, false
	}
	for _, k := range []string{"tool_input", "toolInput"} {
		in, has := ev[k]
		if !has {
			continue
		}
		var v any
		if json.Unmarshal(in, &v) != nil {
			continue
		}
		m := &editMeasure{target: target}
		m.collectNew(v)
		n, found := m.sum(v)
		if m.unmeasurable {
			return 0, false
		}
		return n, found
	}
	return 0, false
}

var (
	editOldKeys  = []string{"old_string", "oldString", "old_text", "oldText", "old_str"}
	editNewKeys  = []string{"new_string", "newString", "new_text", "newText", "new_str"}
	editBodyKeys = []string{"content", "contents", "text", "file_text", "new_source"}
	// editReplaceAllKeys are the flags that make one edit rewrite every match.
	editReplaceAllKeys = []string{"replace_all", "replaceAll", "replace_all_occurrences", "replaceAllOccurrences"}
)

// editMeasure carries what measuring an event needs beyond the event itself.
type editMeasure struct {
	target       string
	newText      strings.Builder // every new-text string in the event
	file         string
	fileRead     bool
	unmeasurable bool
}

func (m *editMeasure) collectNew(v any) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			m.collectNew(e)
		}
	case map[string]any:
		for _, k := range editNewKeys {
			if s, ok := t[k].(string); ok {
				m.newText.WriteString(s)
				m.newText.WriteByte('\n')
			}
		}
		for _, c := range t {
			switch c.(type) {
			case []any, map[string]any:
				m.collectNew(c)
			}
		}
	}
}

// occurrences counts old in the target file and in the event's own new text. An
// empty pattern or an unreadable file makes the reach unknowable.
func (m *editMeasure) occurrences(old string) int {
	if !m.fileRead {
		m.fileRead = true
		b, err := os.ReadFile(m.target)
		if err != nil {
			m.unmeasurable = true
		}
		m.file = string(b)
	}
	if old == "" {
		m.unmeasurable = true
	}
	if m.unmeasurable {
		return 0
	}
	return strings.Count(m.file, old) + strings.Count(m.newText.String(), old)
}

func isKnownReplaceAllKey(k string) bool {
	for _, known := range editReplaceAllKeys {
		if k == known {
			return true
		}
	}
	return false
}

// normKey lowercases k and drops separators, so replace-all, Replace_All and
// replaceAll compare equal.
func normKey(k string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(k))
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	}
	return false
}

func (m *editMeasure) sum(v any) (int, bool) {
	switch t := v.(type) {
	case []any:
		total, found := 0, false
		for _, e := range t {
			n, ok := m.sum(e)
			total, found = total+n, found || ok
		}
		return total, found
	case map[string]any:
		size, found := 0, false
		for _, keys := range [][]string{editOldKeys, editNewKeys, editBodyKeys} {
			for _, k := range keys {
				if s, ok := t[k].(string); ok {
					found = true
					if n := countLines(s); n > size {
						size = n
					}
				}
			}
		}
		for _, k := range editReplaceAllKeys {
			if !truthy(t[k]) {
				continue
			}
			reach, sawOld := 1, false
			for _, ok := range editOldKeys {
				if old, has := t[ok].(string); has {
					sawOld = true
					if n := m.occurrences(old); n > reach {
						reach = n
					}
				}
			}
			// A replace-all with no old text under a key this table knows has a
			// reach nobody can count (the text to match is somewhere unrecognised):
			// unmeasurable, never "one occurrence".
			if !sawOld {
				m.unmeasurable = true
			}
			size *= reach
			break
		}
		// A harness may name its replace-every-match flag something this table has
		// never seen. A truthy flag that reads like "all"/"global" under an
		// unrecognised key is a replace-all whose reach is unknown: fail closed,
		// rather than measure it as one edit.
		for k, val := range t {
			if isKnownReplaceAllKey(k) || !truthy(val) {
				continue
			}
			if n := normKey(k); strings.Contains(n, "all") || strings.Contains(n, "global") {
				m.unmeasurable = true
			}
		}
		total := size
		for _, child := range t {
			switch child.(type) {
			case []any, map[string]any:
				n, ok := m.sum(child)
				total, found = total+n, found || ok
			}
		}
		return total, found
	}
	return 0, false
}

// countLines is the number of lines in s, ignoring one trailing newline; empty
// text is zero lines.
func countLines(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
