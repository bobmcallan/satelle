package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The verbatim-duplication threshold is configuration: the binary ships a
// default in doctor.toml and a repo overrides it under [doctor].
func TestDoctorDuplicateMinWordsDefaultAndOverride(t *testing.T) {
	if err := EmbeddedDoctorErr(); err != nil {
		t.Fatalf("embedded doctor.toml: %v", err)
	}
	def := EmbeddedDoctor().DuplicateMinWords
	if def <= 0 {
		t.Fatalf("the embedded default must be positive, got %d", def)
	}
	if got := (Config{}).ResolveDuplicateMinWords(); got != def {
		t.Errorf("no repo value: got %d, want the embedded default %d", got, def)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "satelle.toml")
	if err := os.WriteFile(path, []byte("[doctor]\nduplicate_min_words = 31\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ResolveDuplicateMinWords(); got != 31 {
		t.Errorf("repo override: got %d, want 31", got)
	}

	// An unset or non-positive repo value falls through to the default.
	if got := (Config{Doctor: DoctorConfig{DuplicateMinWords: -4}}).ResolveDuplicateMinWords(); got != def {
		t.Errorf("non-positive value must fall back to the default, got %d", got)
	}
}
