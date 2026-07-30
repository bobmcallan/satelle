//go:build plannerbench

package plannerbench

import "math/rand"

// The work list is built in full and then SHUFFLED under the study's recorded
// seed. A nested loop (every run of binding A, then every run of binding B)
// confounds run order with binding: whichever binding runs last absorbs every
// warm-cache, thermal and rate-limit effect of the whole study. Randomizing the
// order spreads those effects across cells, and recording each sample's global
// run_order keeps them analysable afterwards.

// scheduleFilters narrows the study to a subset without changing how the
// remainder is ordered.
type scheduleFilters struct {
	Binding string
	Fixture string
}

// buildWorkList returns the sample schedule plus the bindings that could not be
// sampled on this host, each with its reason. A binding whose binary is missing
// is SKIPPED WITH A REASON rather than silently dropped: a comparison that lost
// a side must be able to say why.
func buildWorkList(bindings []studyBinding, fixtures []fixture, runs int, f scheduleFilters) ([]workItem, map[string]string) {
	skipped := map[string]string{}
	var work []workItem
	for _, b := range bindings {
		if f.Binding != "" && f.Binding != b.ID {
			continue
		}
		if reason := b.unavailableReason(); reason != "" {
			skipped[b.ID] = reason
			continue
		}
		for _, fixture := range fixtures {
			if f.Fixture != "" && f.Fixture != fixture.Name {
				continue
			}
			for run := 1; run <= runs; run++ {
				work = append(work, workItem{binding: b, fixture: fixture, run: run})
			}
		}
	}
	return work, skipped
}

// shuffleWork randomizes the schedule deterministically for a given seed, so a
// study is reproducible from its recorded seed alone.
func shuffleWork(work []workItem, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(work), func(i, j int) { work[i], work[j] = work[j], work[i] })
}
