package config

import (
	"strings"
	"testing"
)

func TestCanonicaliseTags_EmptyVocabularyNoOp(t *testing.T) {
	c := Config{}
	in := []string{"surface:ui", "area:web", "bare"}
	got, err := c.CanonicaliseTags(in)
	if err != nil {
		t.Fatalf("empty vocab: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("got %v, want %v", got, in)
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], in[i])
		}
	}
}

func TestCanonicaliseTags_UnknownNamespacePassesThrough(t *testing.T) {
	c := Config{Tags: TagsConfig{Vocabulary: map[string][]string{
		"surface": {"ui", "cli"},
	}}}
	got, err := c.CanonicaliseTags([]string{"area:web", "epic:foo", "bare"})
	if err != nil {
		t.Fatalf("unknown ns: %v", err)
	}
	want := []string{"area:web", "epic:foo", "bare"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCanonicaliseTags_NoColonPassesThrough(t *testing.T) {
	c := Config{Tags: TagsConfig{Vocabulary: map[string][]string{
		"surface": {"ui", "cli"},
	}}}
	got, err := c.CanonicaliseTags([]string{"mvp", "web"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "mvp" || got[1] != "web" {
		t.Fatalf("got %v", got)
	}
}

func TestCanonicaliseTags_EqualFoldAcceptsAndCanonicalises(t *testing.T) {
	c := Config{Tags: TagsConfig{Vocabulary: map[string][]string{
		"surface": {"ui", "cli"},
	}}}
	got, err := c.CanonicaliseTags([]string{"surface:UI", "surface:Cli"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "surface:ui" {
		t.Errorf("surface:UI → %q, want surface:ui", got[0])
	}
	if got[1] != "surface:cli" {
		t.Errorf("surface:Cli → %q, want surface:cli", got[1])
	}
}

func TestCanonicaliseTags_UnknownValueNamedError(t *testing.T) {
	c := Config{Tags: TagsConfig{Vocabulary: map[string][]string{
		"surface": {"ui", "cli"},
	}}}
	_, err := c.CanonicaliseTags([]string{"surface:web"})
	if err == nil {
		t.Fatal("expected error for unknown surface value")
	}
	msg := err.Error()
	// Named error: namespace, rejected value, allowed list.
	for _, want := range []string{"surface", "web", "ui", "cli"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should name %q", msg, want)
		}
	}
}

func TestCanonicaliseTags_MultiSurfacePreserved(t *testing.T) {
	c := Config{Tags: TagsConfig{Vocabulary: map[string][]string{
		"surface": {"ui", "cli"},
	}}}
	got, err := c.CanonicaliseTags([]string{"surface:ui", "surface:cli", "area:web"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "surface:ui" || got[1] != "surface:cli" || got[2] != "area:web" {
		t.Fatalf("got %v", got)
	}
}

func TestCanonicaliseTags_NamespaceCaseInsensitive(t *testing.T) {
	// Config key "Surface" still matches tag "surface:ui".
	c := Config{Tags: TagsConfig{Vocabulary: map[string][]string{
		"Surface": {"UI", "CLI"},
	}}}
	got, err := c.CanonicaliseTags([]string{"surface:ui"})
	if err != nil {
		t.Fatal(err)
	}
	// Canonical namespace + value come from config declaration.
	if got[0] != "Surface:UI" {
		t.Errorf("got %q, want Surface:UI", got[0])
	}
}
