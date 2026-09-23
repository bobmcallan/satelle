package compact

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// memStore is an in-memory Offloader/Resolver for tests — the same content-
// addressed shape internal/retrieve.Store gives production callers.
type memStore struct {
	blobs map[string][]byte
}

func newMemStore() *memStore { return &memStore{blobs: map[string][]byte{}} }

func (m *memStore) Put(content []byte) (string, error) {
	hash := retrieve.Hash(content)
	m.blobs[hash] = append([]byte(nil), content...)
	return hash, nil
}

func (m *memStore) Get(hash string) ([]byte, error) {
	b, ok := m.blobs[hash]
	if !ok {
		return nil, errors.New("memStore: not found")
	}
	return b, nil
}

// decodeRecords unmarshals raw JSON (array of objects) into a comparable form
// — []map[string]any via json.Number, matching the round-trip test's own
// reflect.DeepEqual comparison (sty_75b76691 AC1).
func decodeRecords(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out []map[string]any
	if err := d.Decode(&out); err != nil {
		t.Fatalf("decode records: %v", err)
	}
	return out
}

func TestEncodeDecodeTableRoundTrip(t *testing.T) {
	raw := json.RawMessage(`[
		{"id":"le_1","kind":"gate","story_id":"sty_a","limit":3,"ok":true,"payload":{"a":1},"note":null},
		{"id":"le_2","kind":"gate","story_id":"sty_b","limit":7,"ok":false,"payload":{"b":[1,2]},"note":"hi"}
	]`)
	enc, ok := EncodeTable(raw, 0, nil)
	if !ok {
		t.Fatalf("EncodeTable: not uniform")
	}
	dec, err := DecodeTable(enc, nil)
	if err != nil {
		t.Fatalf("DecodeTable: %v", err)
	}
	want := decodeRecords(t, raw)
	got := decodeRecords(t, dec)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v", want, got)
	}
}

func TestEncodeTableLongCellOffloadAndResolve(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "0123456789"
	}
	raw, err := json.Marshal([]map[string]any{
		{"id": "le_1", "body": long},
		{"id": "le_2", "body": "short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	enc, ok := EncodeTable(raw, 100, store)
	if !ok {
		t.Fatalf("EncodeTable: not uniform")
	}
	if len(store.blobs) != 1 {
		t.Fatalf("expected exactly one offloaded blob, got %d", len(store.blobs))
	}
	dec, err := DecodeTable(enc, store)
	if err != nil {
		t.Fatalf("DecodeTable: %v", err)
	}
	want := decodeRecords(t, raw)
	got := decodeRecords(t, dec)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch after offload\nwant %#v\ngot  %#v", want, got)
	}
}

func TestUniformColumnsRejectsMixedType(t *testing.T) {
	mixedType := json.RawMessage(`[{"a":1},{"a":"one"}]`)
	if _, ok := EncodeTable(mixedType, 0, nil); ok {
		t.Errorf("EncodeTable(mixed type column) = ok, want not uniform")
	}
	nullableOK := json.RawMessage(`[{"a":1,"b":null},{"a":2,"b":"x"}]`)
	if _, ok := EncodeTable(nullableOK, 0, nil); !ok {
		t.Errorf("EncodeTable(nullable column) = not uniform, want ok (null never disqualifies)")
	}
}

// TestEncodeDecodeTableRoundTripSparseKeys (sty_75b76691): a column absent on
// some rows — the ordinary shape of a Go `omitempty` JSON list (e.g.
// ledger.Entry: story_id/actor/body/payload are all omitempty) — still folds,
// and a row that never had the key does not grow one on decode.
func TestEncodeDecodeTableRoundTripSparseKeys(t *testing.T) {
	raw := json.RawMessage(`[
		{"id":"le_1","kind":"gate"},
		{"id":"le_2","kind":"note","body":"hi","actor":"executor"},
		{"id":"le_3","kind":"gate","payload":{"a":1}}
	]`)
	enc, ok := EncodeTable(raw, 0, nil)
	if !ok {
		t.Fatalf("EncodeTable: not uniform")
	}
	dec, err := DecodeTable(enc, nil)
	if err != nil {
		t.Fatalf("DecodeTable: %v", err)
	}
	want := decodeRecords(t, raw)
	got := decodeRecords(t, dec)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v", want, got)
	}
	// le_1 never had "body" — decoding must not have invented the key.
	if _, has := got[0]["body"]; has {
		t.Errorf("row 0 gained a \"body\" key it never had: %#v", got[0])
	}
}

func TestEncodeTableRejectsNonArrayAndEmpty(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`[]`),
		json.RawMessage(``),
		json.RawMessage(`"just a string"`),
	} {
		if _, ok := EncodeTable(raw, 0, nil); ok {
			t.Errorf("EncodeTable(%s) = ok, want false", raw)
		}
	}
}

func TestFoldTableFallsBackWhenNotSmaller(t *testing.T) {
	// A single tiny row: the table header overhead makes the encoding bigger
	// than the original compact JSON, so FoldTable must decline.
	raw := json.RawMessage(`[{"a":1}]`)
	if _, ok := FoldTable(raw, 0, nil, nil); ok {
		t.Errorf("FoldTable(tiny row) = ok, want false (not smaller)")
	}
}

func TestFoldTableFallsBackOnDecodeMismatch(t *testing.T) {
	// A resolver that returns the wrong bytes must make FoldTable refuse the
	// fold rather than silently return a lossy encoding.
	raw, err := json.Marshal([]map[string]any{
		{"id": "a", "body": stringOfLen(300)},
		{"id": "b", "body": stringOfLen(300)},
	})
	if err != nil {
		t.Fatal(err)
	}
	bad := &wrongResolver{store: newMemStore()}
	if _, ok := FoldTable(raw, 50, bad, bad); ok {
		t.Errorf("FoldTable with mismatching resolver = ok, want false")
	}
}

type wrongResolver struct{ store *memStore }

func (w *wrongResolver) Put(content []byte) (string, error) { return w.store.Put(content) }
func (w *wrongResolver) Get(hash string) ([]byte, error)    { return []byte("WRONG BYTES"), nil }

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(i%26)
	}
	return string(b)
}
