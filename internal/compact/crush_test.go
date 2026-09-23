package compact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

func crushTestConfig() CrushConfig {
	return CrushConfig{
		MinItems:                  5,
		SizeThresholdBytes:        100,
		MaxKept:                   10,
		FirstFraction:             0.3,
		LastFraction:              0.15,
		VarianceSigma:             2.0,
		StructuralOutlierFraction: 0.2,
		RareStatusFraction:        0.2,
		StatusFields:              []string{"status"},
		ErrorKeywords:             []string{"error", "failed", "panic"},
	}
}

// crushFixture builds n bland rows; mutate edits the row at planted.
func crushFixture(n, planted int, mutate func(row map[string]any)) (json.RawMessage, []byte) {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": i, "status": "ok", "note": "routine entry", "n": 10 + i%3}
	}
	var want []byte
	if mutate != nil {
		mutate(rows[planted])
		want, _ = json.Marshal(rows[planted])
	}
	raw, _ := json.Marshal(rows)
	return raw, want
}

func crushed(t *testing.T, raw json.RawMessage, cfg CrushConfig) ([]json.RawMessage, *memStore) {
	t.Helper()
	st := newMemStore()
	out, ok := CrushArray(raw, cfg, st)
	if !ok {
		t.Fatalf("CrushArray did not crush")
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(out, &elems); err != nil {
		t.Fatalf("output not a JSON array: %v", err)
	}
	return elems, st
}

func contains(elems []json.RawMessage, want []byte) bool {
	for _, e := range elems {
		if bytes.Equal(e, want) {
			return true
		}
	}
	return false
}

// TestCrushForcedKeepsSurvive (sty_aa34491d AC2): a planted mid-array row
// positional and stride sampling would miss survives byte-for-byte.
func TestCrushForcedKeepsSurvive(t *testing.T) {
	cases := map[string]func(row map[string]any){
		"error keyword":      func(r map[string]any) { r["note"] = "Build FAILED badly" },
		"numeric outlier":    func(r map[string]any) { r["n"] = 100000 },
		"length outlier":     func(r map[string]any) { r["note"] = strings.Repeat("long ", 400) },
		"structural outlier": func(r map[string]any) { r["extra"] = "only here" },
		"rare status":        func(r map[string]any) { r["status"] = "weird" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			raw, want := crushFixture(200, 101, mutate)
			elems, _ := crushed(t, raw, crushTestConfig())
			if !contains(elems, want) {
				t.Fatalf("planted row dropped; kept %d elements", len(elems))
			}
			if len(elems) > 40 {
				t.Errorf("kept %d elements, crush barely dropped anything", len(elems))
			}
		})
	}
}

// TestCrushErrorKeywordIgnoresKeysAndSubstrings: a key named "error" or a
// substring like "terrorist" is not an error row.
func TestCrushErrorKeywordIgnoresKeysAndSubstrings(t *testing.T) {
	raw, want := crushFixture(200, 101, func(r map[string]any) { r["error"] = ""; r["note"] = "terrors entry" })
	cfg := crushTestConfig()
	cfg.StructuralOutlierFraction, cfg.VarianceSigma = 0, 0
	elems, _ := crushed(t, raw, cfg)
	if contains(elems, want) {
		t.Fatalf("row kept only because of a key/substring match")
	}
}

// TestCrushForcedOutsideBudget: K=1 with several forced rows keeps them all.
func TestCrushForcedOutsideBudget(t *testing.T) {
	rows := make([]map[string]any, 100)
	for i := range rows {
		rows[i] = map[string]any{"id": i, "status": "ok"}
	}
	for _, i := range []int{20, 40, 60, 80} {
		rows[i]["status"] = fmt.Sprintf("odd%d", i)
	}
	raw, _ := json.Marshal(rows)
	cfg := crushTestConfig()
	cfg.MaxKept = 1
	elems, _ := crushed(t, raw, cfg)
	for _, i := range []int{20, 40, 60, 80} {
		w, _ := json.Marshal(rows[i])
		if !contains(elems, w) {
			t.Errorf("forced row %d dropped", i)
		}
	}
}

// TestCrushMarkerResolvesToFullOriginal (AC3).
func TestCrushMarkerResolvesToFullOriginal(t *testing.T) {
	raw, _ := crushFixture(200, 101, func(r map[string]any) { r["note"] = "panic: boom" })
	elems, st := crushed(t, raw, crushTestConfig())

	var marker struct {
		Crushed string `json:"_crushed"`
		Dropped int    `json:"dropped"`
		Total   int    `json:"total"`
	}
	last := elems[len(elems)-1]
	if err := json.Unmarshal(last, &marker); err != nil || marker.Crushed == "" {
		t.Fatalf("last element is not the marker: %s", last)
	}
	if marker.Total != 200 || marker.Dropped != 200-(len(elems)-1) {
		t.Errorf("marker counts %+v inconsistent with %d kept", marker, len(elems)-1)
	}
	hashes := retrieve.FindHashes(marker.Crushed)
	if len(hashes) != 1 {
		t.Fatalf("marker %q carries %d hashes", marker.Crushed, len(hashes))
	}
	full, err := st.Get(hashes[0])
	if err != nil || !jsonEqual(full, raw) {
		t.Fatalf("marker does not resolve to the full original array (err=%v)", err)
	}
	// Every kept row is a whole, unmodified original object, in order.
	var orig []json.RawMessage
	_ = json.Unmarshal(raw, &orig)
	pos := 0
	for _, e := range elems[:len(elems)-1] {
		for pos < len(orig) && !bytes.Equal(orig[pos], e) {
			pos++
		}
		if pos == len(orig) {
			t.Fatalf("kept row %s not an original row in original order", e)
		}
		pos++
	}
}

// TestCrushBailOuts: below the minimum, no signal, disabled, non-objects.
func TestCrushBailOuts(t *testing.T) {
	st := newMemStore()
	raw4, _ := crushFixture(4, 1, func(r map[string]any) { r["note"] = "error" })
	if _, ok := CrushArray(raw4, crushTestConfig(), st); ok {
		t.Error("crushed an array below MinItems")
	}
	// Unique entities, nothing forced: skip.
	rows := make([]map[string]any, 100)
	for i := range rows {
		rows[i] = map[string]any{"id": 1000 + i, "name": fmt.Sprintf("entity-%03d", i)}
	}
	unique, _ := json.Marshal(rows)
	if _, ok := CrushArray(unique, crushTestConfig(), st); ok {
		t.Error("crushed unique entities with no signal")
	}
	raw, _ := crushFixture(200, 101, func(r map[string]any) { r["note"] = "error" })
	if _, ok := CrushArray(raw, CrushConfig{}, st); ok {
		t.Error("zero config crushed")
	}
	if _, ok := CrushArray(raw, crushTestConfig(), nil); ok {
		t.Error("crushed with no offloader")
	}
	if _, ok := CrushArray(json.RawMessage(`[1,2,3,4,5,6]`), crushTestConfig(), st); ok {
		t.Error("crushed non-objects")
	}
}

// TestCrushKnobsDriveBehaviour (AC1): each configured value, not a constant,
// changes the outcome.
func TestCrushKnobsDriveBehaviour(t *testing.T) {
	// Plain rows with a rare status only at the very end keep signal alive
	// without forcing mid rows.
	raw, _ := crushFixture(200, 199, func(r map[string]any) { r["status"] = "rare" })
	count := func(cfg CrushConfig) int {
		elems, _ := crushed(t, raw, cfg)
		return len(elems)
	}
	base := count(crushTestConfig())

	k := crushTestConfig()
	k.MaxKept = 40
	if got := count(k); got <= base {
		t.Errorf("MaxKept 40 kept %d, not more than %d", got, base)
	}
	f := crushTestConfig()
	f.FirstFraction, f.LastFraction, f.MaxKept = 0.9, 0.05, 20
	elems, _ := crushed(t, raw, f)
	if !contains(elems, mustRow(raw, 17)) || contains(elems, mustRow(raw, 60)) {
		t.Errorf("FirstFraction 0.9 of K=20 should keep the first 18 rows")
	}
	s := crushTestConfig()
	s.RareStatusFraction = 0.0001
	s.MaxKept = 3
	if got := count(s); got >= base {
		t.Errorf("rare threshold tiny should keep fewer (%d vs %d)", got, base)
	}
	e := crushTestConfig()
	e.ErrorKeywords = []string{"routine"}
	if _, ok := CrushArray(raw, e, newMemStore()); ok {
		t.Error("keyword matching every row force-keeps all of them; nothing should drop")
	}
}

func mustRow(raw json.RawMessage, i int) []byte {
	var rows []json.RawMessage
	_ = json.Unmarshal(raw, &rows)
	return rows[i]
}
