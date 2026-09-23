package cli

import "testing"

// story tidy / untidy are satelle verbs, not tree mutations as far as the Bash
// gate can tell — so a coder or driver running them during in_progress is not
// blocked by the edit fence (sty_d74e9b1b AC3).
func TestStoryTidyIsNotClassifiedAsTreeMutation(t *testing.T) {
	anchor := t.TempDir()
	for _, c := range []string{
		"satelle story tidy sty_x .ac-evidence.txt fixturegen",
		"satelle story tidy sty_x " + anchor + "/stray.txt",
		"satelle story untidy sty_x --all",
		"cd " + anchor + " && satelle story tidy sty_x wd_debug_test.go",
	} {
		if bashMutatesTree(c, anchor) {
			t.Errorf("bashMutatesTree(%q) = true, want false", c)
		}
	}
}
