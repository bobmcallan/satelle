package compact

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// Column value kinds recorded in a table header.
const (
	KindStr  = "str"
	KindNum  = "num"
	KindBool = "bool"
	KindNull = "null"
	KindJSON = "json"
)

var tableHeaderRE = regexp.MustCompile(`^\[(\d+)\]\{(.*)\}$`)

// EncodeTable renders raw — a JSON array of objects with one JSON kind per
// column across every row it appears in (a null value never disqualifies or
// fixes a column's kind, and Go's ubiquitous `omitempty` means a column may
// legitimately be ABSENT on some rows — that never disqualifies the fold
// either; see cellAbsent) — as a compact table: a header line
// "[N]{col:kind,...}" followed by CSV rows (encoding/csv), one row per
// element, columns in the header's order. ok is false when raw is not a JSON
// array of objects, is empty, or some column mixes real (non-null) JSON
// kinds across rows — nothing to fold.
//
// A present cell is the element's exact compact JSON for that field: decoding
// never re-derives a value, it parses the cell back. An absent cell (the key
// missing on that row) is the empty CSV field — distinct from a present JSON
// null, which is the literal 4-byte token `null`; DecodeTable tells the two
// apart and omits the key entirely for an absent cell, so a row that never had
// the key does not grow one. longCellBytes > 0 with a non-nil off offloads any
// present string cell whose JSON literal exceeds that many bytes through off,
// replacing it with a quoted retrieve marker (retrieve.MarkerKind) —
// DecodeTable resolves it back through a Resolver.
func EncodeTable(raw json.RawMessage, longCellBytes int, off Offloader) (string, bool) {
	rows, ok := extractRows(raw)
	if !ok || len(rows) == 0 {
		return "", false
	}
	cols, kinds, ok := uniformColumns(rows)
	if !ok {
		return "", false
	}

	var buf bytes.Buffer
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c + ":" + kinds[c]
	}
	fmt.Fprintf(&buf, "[%d]{%s}\n", len(rows), strings.Join(header, ","))

	w := csv.NewWriter(&buf)
	for _, row := range rows {
		rec := make([]string, len(cols))
		for i, c := range cols {
			v, present := row[c]
			cell := cellAbsent
			if present {
				cell = string(v)
			}
			if off != nil && longCellBytes > 0 && present && kinds[c] == KindStr && len(v) > longCellBytes {
				if enc, ok := offloadCell(v, off); ok {
					cell = enc
				}
			}
			rec[i] = cell
		}
		if err := w.Write(rec); err != nil {
			return "", false
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", false
	}
	return buf.String(), true
}

// cellAbsent is the CSV cell for a key that is missing on a row — the empty
// string. Distinct from a present JSON null (the literal text "null"): a
// string-typed VALUE that happens to be empty is "\"\"" (two quote bytes), so
// there is no collision with a genuine empty string.
const cellAbsent = ""

// offloadCell replaces a long JSON string literal with a quoted retrieve
// marker carrying its kind and original byte size.
func offloadCell(v json.RawMessage, off Offloader) (string, bool) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	hash, err := off.Put([]byte(s))
	if err != nil {
		return "", false
	}
	marker := retrieve.MarkerKind(hash, KindStr, len(s))
	mb, err := marshalNoEscape(marker)
	if err != nil {
		return "", false
	}
	return string(mb), true
}

// marshalNoEscape JSON-encodes v without HTML-escaping '<', '>' and '&' — the
// default json.Marshal escaping would turn a literal retrieve marker
// (<<ccr:HASH...>>) into <<ccr:... in the printed output, which
// still decodes to the same value but is no longer the plain-text-greppable
// marker the rest of the retrieve system promises (Marker/MarkerRE).
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// DecodeTable is EncodeTable's exact inverse: it parses a compact table back
// into the original JSON array. res resolves an offloaded str cell back to
// its original content; nil leaves an offloaded cell as its marker string
// (still valid JSON, just not the original value — decoding without the
// Resolver that encoded it can never reconstruct the original).
func DecodeTable(s string, res Resolver) (json.RawMessage, error) {
	headerLine, body, _ := strings.Cut(s, "\n")
	m := tableHeaderRE.FindStringSubmatch(headerLine)
	if m == nil {
		return nil, fmt.Errorf("compact: table: malformed header %q", headerLine)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return nil, fmt.Errorf("compact: table: bad row count: %w", err)
	}
	var cols []string
	kinds := map[string]string{}
	if spec := strings.TrimSpace(m[2]); spec != "" {
		for _, col := range strings.Split(spec, ",") {
			name, kind, ok := strings.Cut(col, ":")
			if !ok {
				return nil, fmt.Errorf("compact: table: malformed column spec %q", col)
			}
			cols = append(cols, name)
			kinds[name] = kind
		}
	}

	arr := make([]json.RawMessage, 0, n)
	if strings.TrimSpace(body) != "" {
		r := csv.NewReader(strings.NewReader(body))
		r.FieldsPerRecord = len(cols)
		for {
			rec, rerr := r.Read()
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return nil, fmt.Errorf("compact: table: csv: %w", rerr)
			}
			obj := make(map[string]json.RawMessage, len(cols))
			for i, c := range cols {
				if rec[i] == cellAbsent {
					continue // the key was missing on this row — leave it out
				}
				obj[c] = resolveCell(rec[i], kinds[c], res)
			}
			b, merr := json.Marshal(obj)
			if merr != nil {
				return nil, merr
			}
			arr = append(arr, b)
		}
	}
	if len(arr) != n {
		return nil, fmt.Errorf("compact: table: header declared %d rows, decoded %d", n, len(arr))
	}
	return json.Marshal(arr)
}

func resolveCell(cell, kind string, res Resolver) json.RawMessage {
	if kind == KindStr && res != nil {
		var sv string
		if err := json.Unmarshal([]byte(cell), &sv); err == nil {
			if hash, k, _, ok := retrieve.ParseMarkerKind(sv); ok && k == KindStr {
				if b, gerr := res.Get(hash); gerr == nil {
					if enc, merr := json.Marshal(string(b)); merr == nil {
						return json.RawMessage(enc)
					}
				}
			}
		}
	}
	return json.RawMessage(cell)
}

// FoldTable encodes raw as a table and returns it only when decoding the
// encoding back (through res) reproduces raw's exact JSON value and the
// encoding is smaller than raw; otherwise ok is false and the caller keeps
// printing raw as-is (AC3). Note: a cell offloaded during a failed attempt
// still leaves its bytes in the retrieval store — a harmless orphan blob
// (content-addressed, eligible for the same retention as any other), not a
// correctness issue.
func FoldTable(raw json.RawMessage, longCellBytes int, off Offloader, res Resolver) (string, bool) {
	enc, ok := EncodeTable(raw, longCellBytes, off)
	if !ok || len(enc) >= len(raw) {
		return "", false
	}
	dec, err := DecodeTable(enc, res)
	if err != nil || !jsonEqual(raw, dec) {
		return "", false
	}
	return enc, true
}

func jsonEqual(a, b json.RawMessage) bool {
	av, aok := canonicalize(a)
	bv, bok := canonicalize(b)
	return aok && bok && reflect.DeepEqual(av, bv)
}

func canonicalize(raw json.RawMessage) (any, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// extractRows unmarshals raw as a JSON array of objects, keyed by field for
// exact per-cell byte access. ok is false when raw is not a JSON array, or
// any element is not a JSON object.
func extractRows(raw json.RawMessage) (rows []map[string]json.RawMessage, ok bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	for _, row := range arr {
		if row == nil {
			return nil, false
		}
	}
	return arr, true
}

// uniformColumns computes the sorted union of keys across every row and each
// column's JSON kind. A row need not carry every column — Go's `omitempty`
// means most of this codebase's list responses are legitimately sparse — so
// only a REAL (non-null, present) type mismatch within one column disqualifies
// the whole table ("not uniform"); a column absent on some rows, or null on
// some, is fine.
func uniformColumns(rows []map[string]json.RawMessage) (cols []string, kinds map[string]string, ok bool) {
	if len(rows) == 0 {
		return nil, nil, false
	}
	set := map[string]bool{}
	kinds = map[string]string{}
	for _, row := range rows {
		for c, v := range row {
			set[c] = true
			k := jsonKind(v)
			if k == KindNull {
				continue
			}
			if existing, has := kinds[c]; has && existing != k {
				return nil, nil, false
			}
			kinds[c] = k
		}
	}
	cols = make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
		if _, has := kinds[c]; !has {
			kinds[c] = KindNull
		}
	}
	sort.Strings(cols)
	return cols, kinds, true
}

func jsonKind(v json.RawMessage) string {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return KindNull
	}
	switch t[0] {
	case '"':
		return KindStr
	case '{', '[':
		return KindJSON
	case 't', 'f':
		return KindBool
	case 'n':
		return KindNull
	default:
		return KindNum
	}
}
