package verb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_034d843c: controlled tag vocabulary at create/set.

func surfaceVocab() config.Config {
	return config.Config{Tags: config.TagsConfig{Vocabulary: map[string][]string{
		"surface": {"ui", "cli"},
	}}}
}

func TestTagVocab_CreateRejectsUnknown(t *testing.T) {
	wire(t)
	verb.SetTagVocabulary(surfaceVocab())
	t.Cleanup(verb.ClearTagVocabulary)

	_, err := dispatchRaw(t, "story-create", map[string]any{
		"title": "x", "category": "feature", "tags": []string{"surface:web"},
	})
	if err == nil {
		t.Fatal("expected reject for surface:web")
	}
	msg := err.Error()
	for _, want := range []string{"surface", "web", "ui", "cli"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should name %q", msg, want)
		}
	}
}

func TestTagVocab_CreateAcceptsMultiSurface(t *testing.T) {
	wire(t)
	verb.SetTagVocabulary(surfaceVocab())
	t.Cleanup(verb.ClearTagVocabulary)

	raw := call(t, "story-create", map[string]any{
		"title": "dual", "category": "feature",
		"tags": []string{"surface:ui", "surface:cli"},
	})
	var it struct {
		ID   string   `json:"id"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	hasUI, hasCLI := false, false
	for _, tg := range it.Tags {
		if tg == "surface:ui" {
			hasUI = true
		}
		if tg == "surface:cli" {
			hasCLI = true
		}
	}
	if !hasUI || !hasCLI {
		t.Fatalf("want both surface tags, got %v", it.Tags)
	}
}

func TestTagVocab_SetCanonicalises(t *testing.T) {
	wire(t)
	verb.SetTagVocabulary(surfaceVocab())
	t.Cleanup(verb.ClearTagVocabulary)

	raw := call(t, "story-create", map[string]any{
		"title": "canon", "category": "feature",
	})
	var it struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	raw = call(t, "story-set", map[string]any{
		"id": it.ID, "add_tags": []string{"surface:UI"},
	})
	var updated struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tg := range updated.Tags {
		if tg == "surface:ui" {
			found = true
		}
		if tg == "surface:UI" {
			t.Errorf("stored non-canonical form %q", tg)
		}
	}
	if !found {
		t.Fatalf("want surface:ui after set, got %v", updated.Tags)
	}
}

func TestTagVocab_UnwiredNoOp(t *testing.T) {
	wire(t)
	verb.ClearTagVocabulary() // ensure unwired

	// Without a wired vocabulary, surface:web is free-form and accepted.
	raw := call(t, "story-create", map[string]any{
		"title": "free", "category": "feature", "tags": []string{"surface:web"},
	})
	var it struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tg := range it.Tags {
		if tg == "surface:web" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unwired must accept free-form surface:web, got %v", it.Tags)
	}
}

func TestTagVocab_NoSurfaceTagValid(t *testing.T) {
	wire(t)
	verb.SetTagVocabulary(surfaceVocab())
	t.Cleanup(verb.ClearTagVocabulary)

	raw := call(t, "story-create", map[string]any{
		"title": "plain", "category": "feature", "tags": []string{"area:web"},
	})
	var it struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		t.Fatal(err)
	}
	// area: free-form; no surface: required.
	found := false
	for _, tg := range it.Tags {
		if tg == "area:web" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want area:web preserved, got %v", it.Tags)
	}
}
