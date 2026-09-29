package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestInjectedConfigDecodesAndResolves(t *testing.T) {
	var c Config
	body := `
[instruction_review]
extra_paths = ["a/**", "b/*.md"]

[validate.injected]
bytes_per_token = 3

[validate.injected.budget]
driver = 100
"reviewer[claude/stream]" = 200
`
	if _, err := toml.Decode(body, &c); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.InstructionReview.ExtraPaths, ",") != "a/**,b/*.md" {
		t.Errorf("extra_paths = %v", c.InstructionReview.ExtraPaths)
	}
	inj := c.Validate.Injected
	if got := inj.ResolveBytesPerToken(); got != 3 {
		t.Errorf("bytes_per_token = %d, want 3", got)
	}
	for label, want := range map[string]int{"driver[claude]": 100, "driver": 100, "reviewer[claude/stream]": 200} {
		if got, ok := inj.BudgetFor(label); !ok || got != want {
			t.Errorf("BudgetFor(%q) = %d,%v want %d", label, got, ok, want)
		}
	}
	// A row with no key has no budget: the binary ships none.
	if got, ok := inj.BudgetFor("coder[claude/stream]"); ok {
		t.Errorf("an unset budget must not resolve, got %d", got)
	}
}

func TestInjectedConfigDefaults(t *testing.T) {
	var inj InjectedConfig
	if got := inj.ResolveBytesPerToken(); got != DefaultBytesPerToken {
		t.Errorf("default ratio = %d, want %d", got, DefaultBytesPerToken)
	}
	if _, ok := inj.BudgetFor("driver"); ok {
		t.Error("the zero config must carry no budget")
	}
	inj.BytesPerToken = -5
	if got := inj.ResolveBytesPerToken(); got != DefaultBytesPerToken {
		t.Errorf("a non-positive ratio must fall back, got %d", got)
	}
}
