package config

import "testing"

func TestEmbeddedDoneIsLastPrinciple(t *testing.T) {
	var found bool
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "principles" && d.Name == "satelle-done-is-last" {
			found = true
			if len(d.Body) == 0 {
				t.Fatal("done-is-last principle has empty body")
			}
		}
	}
	if !found {
		t.Fatal("embedded principle satelle-done-is-last not found in EmbeddedDefaults()")
	}
}
