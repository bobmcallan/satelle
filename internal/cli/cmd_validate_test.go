package cli

import "testing"

func TestReservedKeepFile(t *testing.T) {
	for _, fn := range []string{"README.md", "index.md", "log.md"} {
		if !reservedKeepFile(fn) {
			t.Errorf("%s should be a reserved keep-file (exempt)", fn)
		}
	}
	for _, fn := range []string{"satelle-story-review.md", "my-skill.md", "anything.md"} {
		if reservedKeepFile(fn) {
			t.Errorf("%s must NOT be exempt — it is an authored doc", fn)
		}
	}
}
