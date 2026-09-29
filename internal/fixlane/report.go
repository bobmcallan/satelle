package fixlane

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// SizeDist summarises a set of sizes, in changed lines.
type SizeDist struct {
	Count  int         `json:"count"`
	Total  int         `json:"total"`
	Min    int         `json:"min"`
	Max    int         `json:"max"`
	Median int         `json:"median"`
	Counts map[int]int `json:"counts,omitempty"` // size -> how many claims declared it
}

// Figures is what the rows yield over a window: the numbers that make the bound
// enforceable rather than aspirational.
type Figures struct {
	Granted        int            `json:"granted"` // recorded claims
	Refused        int            `json:"refused"` // refused claims
	RefusedByClass map[string]int `json:"refused_by_class"`
	Consumed       int            `json:"consumed"`        // granted claims an edit used
	Bound          SizeDist       `json:"bound"`           // declared size bounds of granted claims
	Used           SizeDist       `json:"used"`            // measured size of the edits claims licensed
	RejectsByGate  map[string]int `json:"rejects_by_gate"` // review_reject rows by gate skill
}

// Claims is the claim count: every claim recorded, granted or refused.
func (f Figures) Claims() int { return f.Granted + f.Refused }

// Compute derives the figures from ledger rows falling in [since, until] (a zero
// bound is open). It reads only what the rows carry — no other source — so any
// window over the ledger reproduces the same numbers.
func Compute(entries []ledger.Entry, since, until time.Time) Figures {
	f := Figures{RefusedByClass: map[string]int{}, RejectsByGate: map[string]int{}}
	var bounds, used []int
	for _, e := range entries {
		if !since.IsZero() && e.CreatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && e.CreatedAt.After(until) {
			continue
		}
		switch e.Kind {
		case ledger.KindFixClaim:
			var p ClaimPayload
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			if p.Decision == DecisionRefused {
				f.Refused++
				f.RefusedByClass[p.RefusedClass]++
				continue
			}
			f.Granted++
			bounds = append(bounds, p.BoundLines)
		case ledger.KindFixClaimUse:
			var p UsePayload
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			f.Consumed++
			used = append(used, p.Lines)
		case ledger.KindReviewReject:
			var p struct {
				Skill string `json:"skill"`
			}
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			f.RejectsByGate[p.Skill]++
		}
	}
	f.Bound, f.Used = dist(bounds), dist(used)
	return f
}

func dist(sizes []int) SizeDist {
	d := SizeDist{Count: len(sizes)}
	if len(sizes) == 0 {
		return d
	}
	sort.Ints(sizes)
	d.Min, d.Max, d.Median = sizes[0], sizes[len(sizes)-1], sizes[len(sizes)/2]
	d.Counts = map[int]int{}
	for _, s := range sizes {
		d.Total += s
		d.Counts[s]++
	}
	return d
}

// Report reads every fix-lane row and every review_reject row from the ledger
// and computes the figures for the window — the same call the CLI makes, so a
// real ledger and a synthetic one are analysed by one function.
func Report(ctx context.Context, ls *ledger.Store, since, until time.Time) (Figures, error) {
	var all []ledger.Entry
	for _, kind := range []string{ledger.KindFixClaim, ledger.KindFixClaimUse, ledger.KindReviewReject} {
		if err := ls.ForEachKind(ctx, "", kind, func(e ledger.Entry) error {
			all = append(all, e)
			return nil
		}); err != nil {
			return Figures{}, err
		}
	}
	return Compute(all, since, until), nil
}
