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

func TestEmbeddedConfigurationOverCodePrinciple(t *testing.T) {
	var found bool
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "principles" && d.Name == "satelle-configuration-over-code" {
			found = true
			if len(d.Body) == 0 {
				t.Fatal("configuration-over-code principle has empty body")
			}
		}
	}
	if !found {
		t.Fatal("embedded principle satelle-configuration-over-code not found in EmbeddedDefaults()")
	}
}

func TestEmbeddedStructureReviewers(t *testing.T) {
	want := map[string]bool{
		"satelle-skill-review":     false,
		"satelle-workflow-review":  false,
		"satelle-principle-review": false,
		"satelle-story-review":     false,
	}
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" {
			if _, ok := want[d.Name]; ok {
				want[d.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("embedded structure reviewer %q missing from EmbeddedDefaults()", name)
		}
	}
}
