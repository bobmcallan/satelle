package compact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// CrushConfig configures CrushArray's lossy sampling of an oversized JSON
// array of objects. Every field is authored configuration
// (internal/config.CrushConfig) — this package carries no default, keyword or
// threshold of its own. A zero CrushConfig (MaxKept<=0 or MinItems<=0) is a
// no-op, the same zero-means-off stance RankConfig takes.
type CrushConfig struct {
	// MinItems: an array with fewer rows is never crushed.
	MinItems int
	// SizeThresholdBytes is the rendered-size budget the CALLER compares the
	// lossless form against before invoking CrushArray (see Over).
	SizeThresholdBytes int
	// MaxKept (K) is the sampling budget: the first/last fractions of it are
	// positional keeps, the remainder is stride-sampled. Forced keeps sit
	// outside it.
	MaxKept       int
	FirstFraction float64
	LastFraction  float64
	// VarianceSigma: a row whose byte length, or any numeric field, lies
	// beyond this many standard deviations from the mean is always kept.
	VarianceSigma float64
	// StructuralOutlierFraction: a row carrying a key present in fewer than
	// this fraction of all rows is always kept.
	StructuralOutlierFraction float64
	// RareStatusFraction: a row whose StatusFields value occurs in fewer than
	// this fraction of the rows carrying that field is always kept.
	RareStatusFraction float64
	StatusFields       []string
	// ErrorKeywords: a row with a string value containing one of these
	// (case-insensitive, whole word) is always kept.
	ErrorKeywords []string
}

// Enabled reports whether cfg asks for crushing at all.
func (c CrushConfig) Enabled() bool { return c.MaxKept > 0 && c.MinItems > 0 }

// Over reports whether size exceeds the configured budget.
func (c CrushConfig) Over(size int) bool { return c.Enabled() && size > c.SizeThresholdBytes }

// CrushMarkerKey is the field of the single trailing element that marks a
// crushed array; the element is the only one that is not an original row.
const CrushMarkerKey = "_crushed"

// CrushArray reduces a JSON array of objects to cfg's budget. Kept rows are
// emitted byte-for-byte, in original order, followed by ONE trailing element
// {"_crushed":"<<ccr:HASH,rows,SIZE>>","dropped":N,"total":T} whose hash
// resolves (satelle retrieve) to the FULL original array. ok is false — raw
// should be used unchanged — when cfg is disabled, off is nil, raw is not an
// array of at least MinItems objects, the array shows no signal (every row a
// unique entity with nothing forced), nothing would be dropped, or the result
// is not smaller.
func CrushArray(raw json.RawMessage, cfg CrushConfig, off Offloader) (json.RawMessage, bool) {
	if off == nil || !cfg.Enabled() {
		return nil, false
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil || len(elems) < cfg.MinItems {
		return nil, false
	}
	n := len(elems)
	rows := make([]map[string]json.RawMessage, n)
	for i, e := range elems {
		e = bytes.TrimSpace(e)
		elems[i] = e
		if len(e) == 0 || e[0] != '{' || json.Unmarshal(e, &rows[i]) != nil || rows[i] == nil {
			return nil, false
		}
	}

	forced := forcedKeeps(elems, rows, cfg)
	statusSignal := hasStatusSignal(rows, cfg)
	if len(forced) == 0 && !statusSignal {
		return nil, false
	}

	keep := make(map[int]bool, len(forced))
	for i := range forced {
		keep[i] = true
	}
	nFirst := int(math.Ceil(cfg.FirstFraction * float64(cfg.MaxKept)))
	nLast := int(math.Ceil(cfg.LastFraction * float64(cfg.MaxKept)))
	for i := 0; i < nFirst && i < n; i++ {
		keep[i] = true
	}
	for i := n - nLast; i < n; i++ {
		if i >= 0 {
			keep[i] = true
		}
	}

	// Fill the remaining budget by stride sampling, deduplicating on exact row
	// bytes so an identical row is never sampled twice.
	budget := cfg.MaxKept - nFirst - nLast
	if budget > 0 {
		seen := map[string]bool{}
		for i := range keep {
			seen[string(elems[i])] = true
		}
		var cands []int
		for i := 0; i < n; i++ {
			if keep[i] || seen[string(elems[i])] {
				continue
			}
			seen[string(elems[i])] = true
			cands = append(cands, i)
		}
		if budget >= len(cands) {
			for _, i := range cands {
				keep[i] = true
			}
		} else {
			for j := 0; j < budget; j++ {
				keep[cands[j*len(cands)/budget]] = true
			}
		}
	}

	dropped := n - len(keep)
	if dropped <= 0 {
		return nil, false
	}
	hash, err := off.Put(raw)
	if err != nil {
		return nil, false
	}
	idx := make([]int, 0, len(keep))
	for i := range keep {
		idx = append(idx, i)
	}
	sort.Ints(idx)

	var b bytes.Buffer
	b.WriteByte('[')
	for _, i := range idx {
		b.Write(elems[i])
		b.WriteByte(',')
	}
	fmt.Fprintf(&b, `{%q:%q,"dropped":%d,"total":%d}]`,
		CrushMarkerKey, retrieve.MarkerKind(hash, "rows", len(raw)), dropped, n)
	if b.Len() >= len(raw) {
		return nil, false
	}
	return b.Bytes(), true
}

// forcedKeeps returns the row indices that are always kept: error keywords,
// length and numeric outliers, structural outliers and rare status values.
func forcedKeeps(elems []json.RawMessage, rows []map[string]json.RawMessage, cfg CrushConfig) map[int]bool {
	n := len(elems)
	forced := map[int]bool{}

	if re := keywordRE(cfg.ErrorKeywords); re != nil {
		for i, row := range rows {
			if rowHasMatch(row, re) {
				forced[i] = true
			}
		}
	}

	if cfg.VarianceSigma > 0 {
		lens := make([]float64, n)
		for i, e := range elems {
			lens[i] = float64(len(e))
		}
		for _, i := range sigmaOutliers(lens, cfg.VarianceSigma) {
			forced[i] = true
		}
		// Numeric fields, in sorted key order for determinism.
		series := map[string][]float64{}
		at := map[string][]int{}
		for i, row := range rows {
			for k, v := range row {
				if f, ok := numberValue(v); ok {
					series[k] = append(series[k], f)
					at[k] = append(at[k], i)
				}
			}
		}
		for k, vals := range series {
			if len(vals) < cfg.MinItems {
				continue
			}
			for _, j := range sigmaOutliers(vals, cfg.VarianceSigma) {
				forced[at[k][j]] = true
			}
		}
	}

	if cfg.StructuralOutlierFraction > 0 {
		count := map[string]int{}
		for _, row := range rows {
			for k := range row {
				count[k]++
			}
		}
		for i, row := range rows {
			for k := range row {
				if float64(count[k]) < cfg.StructuralOutlierFraction*float64(n) {
					forced[i] = true
					break
				}
			}
		}
	}

	if cfg.RareStatusFraction > 0 {
		for _, field := range cfg.StatusFields {
			freq, with, ok := statusFreq(rows, field)
			if !ok {
				continue
			}
			for i, row := range rows {
				if v, has := row[field]; has && float64(freq[string(v)]) < cfg.RareStatusFraction*float64(with) {
					forced[i] = true
				}
			}
		}
	}
	return forced
}

// statusFreq counts each value of field across the rows carrying it. ok is
// false when field is not status-like: absent, or a high-cardinality field
// (values mostly unique), where "rare" would just mean "every row".
func statusFreq(rows []map[string]json.RawMessage, field string) (freq map[string]int, with int, ok bool) {
	freq = map[string]int{}
	for _, row := range rows {
		if v, has := row[field]; has {
			freq[string(v)]++
			with++
		}
	}
	if with == 0 || len(freq)*2 > with {
		return nil, 0, false
	}
	return freq, with, true
}

// hasStatusSignal reports whether any configured status field is status-like,
// i.e. rows are not all unique entities.
func hasStatusSignal(rows []map[string]json.RawMessage, cfg CrushConfig) bool {
	for _, f := range cfg.StatusFields {
		if _, _, ok := statusFreq(rows, f); ok {
			return true
		}
	}
	return false
}

// sigmaOutliers returns the indices of vals lying more than sigma standard
// deviations from the mean. A zero-variance series has none.
func sigmaOutliers(vals []float64, sigma float64) []int {
	if len(vals) < 2 {
		return nil
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	var sq float64
	for _, v := range vals {
		sq += (v - mean) * (v - mean)
	}
	sd := math.Sqrt(sq / float64(len(vals)))
	if sd == 0 {
		return nil
	}
	var out []int
	for i, v := range vals {
		if math.Abs(v-mean) > sigma*sd {
			out = append(out, i)
		}
	}
	return out
}

func numberValue(v json.RawMessage) (float64, bool) {
	t := bytes.TrimSpace(v)
	if len(t) == 0 || (t[0] != '-' && (t[0] < '0' || t[0] > '9')) {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(t, &f); err != nil {
		return 0, false
	}
	return f, true
}

func keywordRE(words []string) *regexp.Regexp {
	var parts []string
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			parts = append(parts, regexp.QuoteMeta(w))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(parts, "|") + `)\b`)
}

// rowHasMatch reports whether any string value in row (at any depth) matches
// re. Keys are not searched: a field NAMED "error" is not an error row.
func rowHasMatch(row map[string]json.RawMessage, re *regexp.Regexp) bool {
	for _, v := range row {
		if valueHasMatch(v, re) {
			return true
		}
	}
	return false
}

func valueHasMatch(v json.RawMessage, re *regexp.Regexp) bool {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return false
	}
	switch t[0] {
	case '"':
		var s string
		return json.Unmarshal(t, &s) == nil && re.MatchString(s)
	case '{':
		var m map[string]json.RawMessage
		return json.Unmarshal(t, &m) == nil && rowHasMatch(m, re)
	case '[':
		var a []json.RawMessage
		if json.Unmarshal(t, &a) != nil {
			return false
		}
		for _, e := range a {
			if valueHasMatch(e, re) {
				return true
			}
		}
	}
	return false
}
