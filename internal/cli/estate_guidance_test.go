package cli

import (
	"strings"
	"testing"
)

// TestEstateGuidanceOnlyAfterACliReplacement (sty_0f471251 AC1): upgrading the
// binary is what stales every other registered repo, so the guidance must appear
// exactly then — and stay quiet for every case that did NOT replace the CLI.
//
// The gate is deliberately `cliReplaced`, not `cliUpdated`: `cliUpdated` is also
// set by a serve-only refresh, which does not stale anything, and printing an
// estate warning there would train the operator to ignore it.
func TestEstateGuidanceOnlyAfterACliReplacement(t *testing.T) {
	for _, tc := range []struct {
		name               string
		cliReplaced, local bool
		want               bool
	}{
		{"cli replaced globally", true, false, true},
		{"no cli replacement (already latest, --check, or serve-only)", false, false, false},
		{"repo-local pin governs one repo, not the estate", true, true, false},
		{"no replacement and local", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			printEstateGuidance(&b, tc.cliReplaced, tc.local)
			got := b.String() != ""
			if got != tc.want {
				t.Fatalf("guidance printed = %v, want %v\ngot output:\n%s", got, tc.want, b.String())
			}
		})
	}
}

// TestEstateGuidanceNamesBothCommands (AC1): the message must name the read-only
// inspection AND the heal, and must distinguish "repos you already have" from
// the new repo the installer's other block is about. A message that only said
// "some repos may be stale" would leave the operator with the same chore.
func TestEstateGuidanceNamesBothCommands(t *testing.T) {
	var b strings.Builder
	printEstateGuidance(&b, true, false)
	got := b.String()

	for _, want := range []string{
		"Repos you already have",
		"satelle doctor --all",
		"satelle init --all",
		"read-only",
		"dry-run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guidance must contain %q, got:\n%s", want, got)
		}
	}
}
