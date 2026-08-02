package config

import (
	"regexp"
	"strings"
	"testing"
)

// The shipped route halves are the first process file a new operator ever reads
// and edits, so they must TEACH their own format (sty_58911b1a) — and, being
// embedded in the binary, they must do it without carrying any one repo's
// vocabulary (satelle-repo-agnostic).
//
// Two nets, deliberately opposite in direction:
//   - TestEmbeddedRouteSourcesTeachTheirFormat asserts the teaching is PRESENT.
//     A ban-list alone would pass an empty file.
//   - TestEmbeddedRouteSourcesAreRepoAgnostic asserts nothing repo-specific
//     leaked in with it.

// commentLines returns the file's comment lines, which is where all the teaching
// and all the leakage risk lives.
func commentLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "<!--") {
			out = append(out, s)
		}
	}
	return out
}

// preambleOf returns everything before the first `## ` record.
func preambleOf(body string) string {
	if i := strings.Index(body, "\n## "); i >= 0 {
		return body[:i]
	}
	return body
}

func TestEmbeddedRouteSourcesTeachTheirFormat(t *testing.T) {
	done, step := embeddedRouteHalves()
	if done == "" || step == "" {
		t.Fatal("the shipped route halves are missing")
	}

	for _, tc := range []struct {
		file, body string
		minBanners int
	}{
		{"done.md", done, 2},
		{"step.md", step, 3},
	} {
		t.Run(tc.file, func(t *testing.T) {
			pre := preambleOf(tc.body)

			// 1. A preamble exists, before any record.
			if !strings.Contains(pre, "HOW TO READ THIS FILE") {
				t.Error("no HOW TO READ THIS FILE preamble — the file must teach its own format")
			}
			// 2. The two-halves model and the linkage rule are stated.
			for _, want := range []string{"done.md", "step.md", "provides:"} {
				if !strings.Contains(pre, want) {
					t.Errorf("preamble does not mention %q — a reader cannot find the other half", want)
				}
			}
			// 3. Route-family banners group the records.
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
			// 4. EVERY record carries provenance. This is the load-bearing assertion:
			//    it fails on a single un-commented section, so the treatment cannot be
			//    applied to most records and quietly skipped on one.
			for _, name := range recordsMissingComment(tc.body) {
				t.Errorf("record %q carries no comment — every section must name what it is for", name)
			}
		})
	}

	// 5. step.md states the specific confusion this epic exists to fix.
	pre := preambleOf(step)
	for _, want := range []string{"STAGE NAME", "provides:"} {
		if !strings.Contains(pre, want) {
			t.Errorf("step.md preamble must state the heading-is-not-identity rule; missing %q", want)
		}
	}
}

// recordsMissingComment returns the `## ` records with no comment line between
// the heading and the record's first content line.
func recordsMissingComment(body string) []string {
	var missing []string
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "## ") {
			continue
		}
		name := strings.TrimSpace(s[3:])
		commented := false
		for j := i + 1; j < len(lines); j++ {
			n := strings.TrimSpace(lines[j])
			if n == "" {
				continue
			}
			if strings.HasPrefix(n, "<!--") {
				commented = true
				break
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
	for _, tc := range []struct{ file, body string }{{"done.md", done}, {"step.md", step}} {
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
