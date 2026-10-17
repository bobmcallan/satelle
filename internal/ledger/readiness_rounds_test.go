package ledger

import "testing"

// sty_a32bd6a9: one rule for whether an attempt was accepted, asked by the
// edge-rounds tally, the reject-budget count and readiness-holds alike.

func performerReject(attempt, notes string) Entry {
	return row(KindPerformerReject, map[string]string{"from": "backlog", "to": "plan", "attempt": attempt, "skill": "plan", "notes": notes})
}

func readinessAccept(attempt string) Entry {
	return row(KindReadinessAccept, map[string]string{"from": "backlog", "to": "plan", "attempt": attempt})
}

func definitionEdit() Entry { return row(KindDefinitionEdited, map[string]string{"field": "body"}) }

// docAttached is a document attachment under name; the type is the name, so a
// reader that matched on type would be fooled where one matching the name is not.
func docAttached(name string) Entry {
	return row(KindStoryDocAttached, map[string]string{"name": name, "type": name})
}

// planDoc is the document name the fixture's performer skill declares.
const planDoc = "design"

func planAttached() Entry { return docAttached(planDoc) }

// counters asks both counters about the backlog→plan edge.
func counters(entries []Entry) (accepted, rejected, budget int) {
	accepted, rejected = EdgeRounds(entries, "backlog", "plan")
	return accepted, rejected, CountRejectedRounds(entries, "backlog", "plan").Rounds
}

func TestAttemptRuleAcrossRoundShapes(t *testing.T) {
	cases := []struct {
		name                         string
		entries                      []Entry
		accepted, rejected, budget   int
		lastAccepted                 bool
		wantAcceptedByAttemptAccepts bool
	}{
		{"accept with reviewers", []Entry{accept("a", "r1"), accept("a", "r2"), readinessAccept("a")}, 1, 0, 0, true, true},
		{"one reviewer rejects", []Entry{accept("a", "r1"), reject("a", "r2", "no")}, 0, 1, 1, false, false},
		{"performer rejects, gate never ran", []Entry{performerReject("a", "wrong premise")}, 0, 1, 1, false, false},
		{"performer failure is a rejected round", []Entry{performerReject("a", "named agent failed")}, 0, 1, 1, false, false},
		{"performer-only accept", []Entry{readinessAccept("a")}, 1, 0, 0, true, true},
		{"accept then performer reject", []Entry{accept("a", "r1"), readinessAccept("a"), performerReject("b", "gone wrong")}, 1, 1, 1, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc, rej, budget := counters(tc.entries)
			if acc != tc.accepted || rej != tc.rejected || budget != tc.budget {
				t.Fatalf("edge rounds = (%d accepted, %d rejected), budget count = %d; want (%d, %d), %d",
					acc, rej, budget, tc.accepted, tc.rejected, tc.budget)
			}
			outs := edgeAttempts(tc.entries, "backlog", "plan")
			last := outs[len(outs)-1]
			if last.Accepted != tc.lastAccepted || last.Rejected == last.Accepted {
				t.Fatalf("last attempt = %+v, want accepted=%v", last, tc.lastAccepted)
			}
			if got := AttemptAccepted(tc.entries, "backlog", "plan", last.Attempt); got != tc.wantAcceptedByAttemptAccepts {
				t.Fatalf("AttemptAccepted(%s) = %v, want %v", last.Attempt, got, tc.wantAcceptedByAttemptAccepts)
			}
		})
	}
}

// An attempt that has written no row yet has nothing that rejects it: the state
// a performer-only round is in when its accept is about to be written.
func TestAttemptAcceptedWithNoRowsYet(t *testing.T) {
	if !AttemptAccepted(nil, "backlog", "plan", "att_new") {
		t.Fatal("an attempt with no rows has no rejecting row")
	}
}

func TestReadinessHolds(t *testing.T) {
	accepted := []Entry{planAttached(), accept("a", "r1"), readinessAccept("a")}
	cases := []struct {
		name    string
		entries []Entry
		holds   bool
	}{
		{"nothing recorded", nil, false},
		{"accepted and unchanged", accepted, true},
		{"reviewers accepted but no readiness accept recorded", []Entry{accept("a", "r1")}, false},
		{"accept then a body edit", append(append([]Entry{}, accepted...), definitionEdit()), false},
		{"accept then a rejected round", append(append([]Entry{}, accepted...), reject("b", "r1", "no")), false},
		{"accept then a performer-rejected round", append(append([]Entry{}, accepted...), performerReject("b", "no")), false},
		{"accept then the declared plan document attached", append(append([]Entry{}, accepted...), planAttached()), false},
		{"accept then an unrelated document named plan", append(append([]Entry{}, accepted...), docAttached("plan")), true},
		{"accept then another accepted round", append(append([]Entry{}, accepted...), accept("b", "r1"), readinessAccept("b")), true},
		{"a definition edit BEFORE the accept does not void it", []Entry{definitionEdit(), planAttached(), accept("a", "r1"), readinessAccept("a")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := ReadinessHolds(tc.entries, "backlog", "plan", planDoc)
			if ok != tc.holds {
				t.Fatalf("ReadinessHolds = %v, want %v", ok, tc.holds)
			}
		})
	}
	// A step whose performer declares no document name is voided by no attach.
	if _, ok := ReadinessHolds(append(append([]Entry{}, accepted...), planAttached(), docAttached("plan")), "backlog", "plan", ""); !ok {
		t.Fatal("with no declared plan document, an attach must not void readiness")
	}
	if attempt, ok := ReadinessHolds(append(append([]Entry{}, accepted...), accept("b", "r1"), readinessAccept("b")), "backlog", "plan", planDoc); !ok || attempt != "b" {
		t.Fatalf("holds names the latest accepted attempt, got %q %v", attempt, ok)
	}
}

// DefinitionEditedSince is DefinitionEditedAfter anchored on a transition: one
// enumeration, two anchors.
func TestDefinitionEditedAfterIsTheSharedEnumeration(t *testing.T) {
	entries := []Entry{definitionEdit(), transition("backlog", "plan"), definitionEdit(), definitionEdit()}
	if got := DefinitionEditedAfter(entries, 1); len(got) != 2 {
		t.Fatalf("after the transition: %d edits, want 2", len(got))
	}
	if got := DefinitionEditedSince(entries, "backlog", "plan"); len(got) != 2 {
		t.Fatalf("DefinitionEditedSince must agree: %d edits, want 2", len(got))
	}
	if DefinitionEditedAfter(entries, -1) != nil || DefinitionEditedAfter(entries, len(entries)) != nil {
		t.Fatal("an anchor outside the entries enumerates nothing")
	}
}
