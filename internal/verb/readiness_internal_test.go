package verb

import (
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// sty_89804a0e: only the LAST change to the acceptance criteria decides whether
// an accepted amendment outranks the plan.
func TestCriteriaLastAmended(t *testing.T) {
	amend := func(field string) ledger.Entry {
		p, _ := json.Marshal(map[string]any{"fields": []AmendField{{Field: field, Old: "a", New: "b"}}})
		return ledger.Entry{Kind: ledger.KindDefinitionAmended, Payload: p}
	}
	edit := func(field string) ledger.Entry {
		p, _ := json.Marshal(definitionEditPayload{Field: field, Old: "a", New: "b"})
		return ledger.Entry{Kind: ledger.KindDefinitionEdited, Payload: p}
	}
	cases := map[string]struct {
		rows []ledger.Entry
		want bool
	}{
		"no rows":                      {nil, false},
		"edit only":                    {[]ledger.Entry{edit("acceptance_criteria")}, false},
		"amend only":                   {[]ledger.Entry{amend("acceptance_criteria")}, true},
		"amend after edit":             {[]ledger.Entry{edit("acceptance_criteria"), amend("acceptance_criteria")}, true},
		"edit after amend":             {[]ledger.Entry{amend("acceptance_criteria"), edit("acceptance_criteria")}, false},
		"amend of another field":       {[]ledger.Entry{amend("title")}, false},
		"other edit after amend":       {[]ledger.Entry{amend("acceptance_criteria"), edit("title")}, true},
		"amend unreadable payload":     {[]ledger.Entry{{Kind: ledger.KindDefinitionAmended, Payload: json.RawMessage("{")}}, false},
		"unrelated row after an amend": {[]ledger.Entry{amend("acceptance_criteria"), {Kind: ledger.KindStatusTransition}}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := criteriaLastAmended(tc.rows); got != tc.want {
				t.Fatalf("criteriaLastAmended = %v, want %v", got, tc.want)
			}
		})
	}
}
