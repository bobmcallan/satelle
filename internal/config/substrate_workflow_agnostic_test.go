package config

import (
	"regexp"
	"strings"
	"testing"
)

// The shipped route halves are the first process file a new operator ever reads
// and edits, so their comments must earn their place — and, being embedded in
// the binary, they must do it without carrying any one repo's vocabulary
// (satelle-repo-agnostic).
//
// Three nets, deliberately opposite in direction:
//   - TestEmbeddedRouteSourcesCarryNoFormatTuition asserts the syntax teaching is
//     GONE. It used to assert the opposite (sty_58911b1a): a "HOW TO READ THIS
//     FILE" preamble was mandatory, because the format was bespoke and existed
//     nowhere else. The route source is TOML now (sty_81bb0dde), and TOML's
//     grammar is not satelle's to teach — a `[table]`, a `#` comment and a
//     quoted key need no preamble. That preamble was 50 lines across the two
//     halves, and none of it ever reached an implementing agent: the dispatch
//     path carries the DERIVED route document, never the source.
//   - TestEmbeddedRouteSourcesStateTheirModel asserts what TOML does NOT supply
//     is still stated. Syntax is self-evident; the MODEL is not. Nothing in TOML
//     says a step's key is the obligation it discharges, or that `status` is a
//     stage name that deliberately repeats. Deleting that with the tuition would
//     be the over-correction this net exists to catch — a ban-list alone would
//     pass an empty file.
//   - TestEmbeddedRouteSourcesAreRepoAgnostic asserts nothing repo-specific
//     leaked in with either.

// commentLines returns the file's comment lines, which is where all the teaching
// and all the leakage risk lives. A TOML comment is `#`.
func commentLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "#") {
			out = append(out, s)
		}
	}
	return out
}

// preambleOf returns everything before the first record table, skipping the
// `[meta]` header the file opens with.
func preambleOf(body string) string {
	rest := body
	if i := strings.Index(body, "\n[meta]"); i >= 0 {
		rest = body[i+len("\n[meta]"):]
	} else if strings.HasPrefix(body, "[meta]") {
		rest = body[len("[meta]"):]
	}
	// The first line that opens a table other than meta ends the preamble.
	lines := strings.Split(rest, "\n")
	for i, ln := range lines {
		if t := strings.TrimSpace(ln); strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "[meta]") {
			return strings.Join(lines[:i], "\n")
		}
	}
	return rest
}

// formatTuition is syntax a reader gets from TOML itself, so a comment
// explaining it is prose the representation made unnecessary (AC4). Each entry
// is a marker of the retired bespoke grammar or of teaching TOML's own rules.
var formatTuition = map[string]string{
	"HOW TO READ THIS FILE": "the preamble the bespoke format needed; TOML needs none",
	"## ":                   "a markdown heading — records are `[table]` now",
	"<!--":                  "an HTML comment — a TOML comment is `#`",
	"one line":              "the one-line-comment convention a real parser makes unnecessary",
	"self-contained on one": "same",
}

func TestEmbeddedRouteSourcesCarryNoFormatTuition(t *testing.T) {
	done, step := embeddedRouteHalves()
	if done == "" || step == "" {
		t.Fatal("the shipped route halves are missing")
	}
	for _, tc := range []struct{ file, body string }{{"done.toml", done}, {"step.toml", step}} {
		t.Run(tc.file, func(t *testing.T) {
			for token, why := range formatTuition {
				if strings.Contains(tc.body, token) {
					t.Errorf("carries format tuition %q (%s) — delete it; the format is TOML's to explain", token, why)
				}
			}
		})
	}
}

func TestEmbeddedRouteSourcesStateTheirModel(t *testing.T) {
	done, step := embeddedRouteHalves()
	if done == "" || step == "" {
		t.Fatal("the shipped route halves are missing")
	}

	for _, tc := range []struct {
		file, body string
		minBanners int
		// wantPreamble are the model rules TOML cannot express, which the file
		// must therefore state before its first record.
		wantPreamble []string
	}{
		{
			file: "done.toml", body: done, minBanners: 2,
			// A category table selects steps by naming their KEYS, and order is
			// derived rather than authored — neither is visible in the syntax.
			wantPreamble: []string{"KEYED BY THE CATEGORY", "step.toml", "ORDER is derived"},
		},
		{
			file: "step.toml", body: step, minBanners: 3,
			// The rule this epic exists to fix: the key is the identity, the
			// `status` is a stage name, and stage names repeat by design.
			wantPreamble: []string{"KEYED BY THE OBLIGATION", "STAGE NAME", "done.toml"},
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			pre := preambleOf(tc.body)
			for _, want := range tc.wantPreamble {
				if !strings.Contains(pre, want) {
					t.Errorf("preamble does not state %q — TOML does not supply it, so the file must", want)
				}
			}
			// Route-family banners group the records.
			banners := 0
			for _, c := range commentLines(tc.body) {
				if strings.Contains(c, "===") {
					banners++
				}
			}
			if banners < tc.minBanners {
				t.Errorf("%d banner comment lines, want at least %d — records must be grouped by route family",
					banners, tc.minBanners)
			}
			// EVERY record carries provenance. This is the load-bearing assertion:
			// it fails on a single un-commented table, so the treatment cannot be
			// applied to most records and quietly skipped on one.
			for _, name := range recordsMissingComment(tc.body) {
				t.Errorf("record %q carries no comment — every table must name what it is for", name)
			}
		})
	}
}

// recordsMissingComment returns the record tables with no comment line between
// the table header and the record's first key. `[meta]` is the document header,
// not a record.
func recordsMissingComment(body string) []string {
	var missing []string
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "[") || strings.HasPrefix(s, "[meta]") {
			continue
		}
		name := s
		commented := false
		// A banner above the table counts: it is the comment naming what the whole
		// route family below it is for.
		for j := i - 1; j >= 0; j-- {
			p := strings.TrimSpace(lines[j])
			if p == "" {
				continue
			}
			if strings.HasPrefix(p, "#") {
				commented = true
			}
			break
		}
		for j := i + 1; j < len(lines) && !commented; j++ {
			n := strings.TrimSpace(lines[j])
			if n == "" {
				continue
			}
			if strings.HasPrefix(n, "#") {
				commented = true
			}
			break // first content line reached
		}
		if !commented {
			missing = append(missing, name)
		}
	}
	return missing
}

var storyID = regexp.MustCompile(`sty_[0-9a-f]{8}`)

func TestEmbeddedRouteSourcesAreRepoAgnostic(t *testing.T) {
	done, step := embeddedRouteHalves()
	if done == "" || step == "" {
		t.Fatal("the shipped route halves are missing")
	}
	// Each entry is banned for a stated reason; keep the list small and precise
	// rather than sprawling, or it starts rejecting legitimate prose.
	banned := map[string]string{
		"this repo":       "the shipped defaults belong to EVERY repo; they cannot refer to one",
		"this repository": "same",
		"our ":            "first person implies an owning team",
		"dogfood":         "a deploy practice of one repo, not a route concept",
		"changelog":       "a release convention of one repo",
		"gofmt":           "a language toolchain of one repo",
		"go test":         "same",
		"github":          "a hosting choice of one repo",
	}
	for _, tc := range []struct{ file, body string }{{"done.toml", done}, {"step.toml", step}} {
		for _, line := range commentLines(tc.body) {
			low := strings.ToLower(line)
			for token, why := range banned {
				if strings.Contains(low, token) {
					t.Errorf("%s: comment carries repo-specific %q (%s):\n  %s", tc.file, token, why, line)
				}
			}
			if m := storyID.FindString(line); m != "" {
				t.Errorf("%s: comment carries story id %s — an id means nothing in another repo:\n  %s",
					tc.file, m, line)
			}
		}
	}
}
