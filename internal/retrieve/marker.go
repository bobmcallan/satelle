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

// Summary returns the human-facing line a compressor appends when it condenses
// n lines down to m: "[N lines compressed to M. Retrieve more: satelle
// retrieve HASH]".
func Summary(hash string, n, m int) string {
	return "[" + strconv.Itoa(n) + " lines compressed to " + strconv.Itoa(m) + ". Retrieve more: satelle retrieve " + hash + "]"
}

// MarkerRE is the single exported detection regex every compressor and reader
// shares, so the marker grammar and its detector never drift apart (AC4). It
// matches both Marker's inline form and the hash embedded in Summary's
// "Retrieve more: satelle retrieve HASH" tail.
var MarkerRE = regexp.MustCompile(`<<ccr:([0-9a-f]{24})>>|Retrieve more: satelle retrieve ([0-9a-f]{24})`)

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
