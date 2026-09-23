package retrieve

import (
	"regexp"
	"strconv"
)

// Marker returns the machine-detectable inline marker a compressor leaves in
// place of dropped content: <<ccr:HASH>>. HASH is the 24-hex-char key Put
// returns for the stored bytes.
func Marker(hash string) string {
	return "<<ccr:" + hash + ">>"
}

// MarkerKind returns the extended inline marker a compactor leaves in place of
// offloaded content, carrying its kind (caller-defined, e.g. "str", "hunk")
// and original byte size: <<ccr:HASH,KIND,SIZE>>. MarkerRE and FindHashes
// recognise both this and the plain Marker form; MarkerKindRE is for a reader
// that needs the kind/size back (sty_75b76691).
func MarkerKind(hash, kind string, size int) string {
	return "<<ccr:" + hash + "," + kind + "," + strconv.Itoa(size) + ">>"
}

// MarkerKindRE matches a whole string that is exactly one MarkerKind marker,
// capturing hash, kind and size. Unlike MarkerRE (presence detection within a
// larger string), this is for a reader that must tell an offloaded cell from
// an ordinary value that merely contains a marker as a substring.
var MarkerKindRE = regexp.MustCompile(`^<<ccr:([0-9a-f]{24}),([^,>]+),(\d+)>>$`)

// ParseMarkerKind decodes a whole string as a MarkerKind marker. ok is false
// when s is not exactly one such marker.
func ParseMarkerKind(s string) (hash, kind string, size int, ok bool) {
	m := MarkerKindRE.FindStringSubmatch(s)
	if m == nil {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return "", "", 0, false
	}
	return m[1], m[2], n, true
}

// Summary returns the human-facing line a compressor appends when it condenses
// n lines down to m: "[N lines compressed to M. Retrieve more: satelle
// retrieve HASH]".
func Summary(hash string, n, m int) string {
	return "[" + strconv.Itoa(n) + " lines compressed to " + strconv.Itoa(m) + ". Retrieve more: satelle retrieve " + hash + "]"
}

// MarkerRE is the single exported detection regex every compressor and reader
// shares, so the marker grammar and its detector never drift apart (AC4). It
// matches both Marker's inline form (with or without MarkerKind's optional
// ",KIND,SIZE" suffix) and the hash embedded in Summary's "Retrieve more:
// satelle retrieve HASH" tail.
var MarkerRE = regexp.MustCompile(`<<ccr:([0-9a-f]{24})(?:,[^,>]+,\d+)?>>|Retrieve more: satelle retrieve ([0-9a-f]{24})`)

// FindHashes returns every hash referenced by a marker or summary line in s,
// in order of appearance, deduplicated.
func FindHashes(s string) []string {
	matches := MarkerRE.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var out []string
	for _, m := range matches {
		hash := m[1]
		if hash == "" {
			hash = m[2]
		}
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		out = append(out, hash)
	}
	return out
}
