package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/compact"
)

// loadToml writes body as a repo's .satelle/satelle.toml and loads it.
func loadToml(t *testing.T, body string) (Config, error) {
	t.Helper()
	satelleDir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(satelleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(satelleDir, ConfigName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	return cfg, err
}

// TestCrushConfigRoundTrip: an enabled [output.crush] table resolves to exactly
// the compact.CrushConfig it authors — every field, lists included (sty_aa34491d).
func TestCrushConfigRoundTrip(t *testing.T) {
	cfg, err := loadToml(t, `[output.crush]
enabled = true
min_items = 7
size_threshold_bytes = 12345
max_kept = 40
first_fraction = 0.25
last_fraction = 0.10
variance_sigma = 2.5
structural_outlier_fraction = 0.15
rare_status_fraction = 0.05
status_fields = ["status", "verdict"]
error_keywords = ["error", "panic"]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := compact.CrushConfig{
		MinItems: 7, SizeThresholdBytes: 12345, MaxKept: 40,
		FirstFraction: 0.25, LastFraction: 0.10, VarianceSigma: 2.5,
		StructuralOutlierFraction: 0.15, RareStatusFraction: 0.05,
		StatusFields:  []string{"status", "verdict"},
		ErrorKeywords: []string{"error", "panic"},
	}
	if got := cfg.Output.Crush.Resolve(); !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve() = %+v, want %+v", got, want)
	}
}

// TestCrushConfigDisabledOrAbsentIsZero: a disabled or absent table resolves
// to the zero compact.CrushConfig — the crusher is off.
func TestCrushConfigDisabledOrAbsentIsZero(t *testing.T) {
	for name, body := range map[string]string{
		"absent":   "retrieve_keep_days = 1\n",
		"disabled": "[output.crush]\nenabled = false\nmin_items = 5\nmax_kept = 60\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadToml(t, body)
			if err != nil {
				t.Fatal(err)
			}
			got := cfg.Output.Crush.Resolve()
			if !reflect.DeepEqual(got, compact.CrushConfig{}) || got.Enabled() {
				t.Errorf("Resolve() = %+v (enabled=%v), want zero and disabled", got, got.Enabled())
			}
		})
	}
}

// TestCrushConfigValidatedAtLoad: nonsensical values refuse the load, naming
// the field.
func TestCrushConfigValidatedAtLoad(t *testing.T) {
	base := "[output.crush]\nenabled = true\nmin_items = 5\nmax_kept = 60\n"
	for name, tc := range map[string]struct{ extra, wantInErr string }{
		"fraction above 1":   {"first_fraction = 1.5\n", "first_fraction"},
		"fraction below 0":   {"rare_status_fraction = -0.1\n", "rare_status_fraction"},
		"first+last above 1": {"first_fraction = 0.7\nlast_fraction = 0.6\n", "first_fraction + last_fraction"},
		"negative max_kept":  {"", "max_kept"},
	} {
		t.Run(name, func(t *testing.T) {
			body := base + tc.extra
			if name == "negative max_kept" {
				body = strings.Replace(base, "max_kept = 60", "max_kept = -1", 1)
			}
			_, err := loadToml(t, body)
			if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("Load error = %v, want one naming %q", err, tc.wantInErr)
			}
		})
	}
}
