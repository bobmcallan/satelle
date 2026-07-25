// Category vocabulary (sty_b2315e17): controlled type list as embedded default
// + satelle.toml override. Values live in substrate/config/categories.toml
// (embedded data, not a Go literal). Enforcement phase is config-driven:
// off | warn | reject (default warn — upgrade never hard-breaks existing flows).
package config

import (
	"fmt"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// CategoriesConfig is the optional [categories] table in satelle.toml.
//
//	[categories]
//	enforce = "warn"                    # off | warn | reject (default warn)
//	extra = ["my-type"]                 # ADD to the embedded default list
//	# vocabulary = ["fix", "feature"]  # REPLACE the embedded default list
type CategoriesConfig struct {
	// Vocabulary, when non-empty, REPLACES the embedded default list entirely.
	Vocabulary []string `toml:"vocabulary"`
	// Extra appends to the resolved base (embedded or Vocabulary).
	Extra []string `toml:"extra"`
	// Enforce is off | warn | reject. Empty means warn.
	Enforce string `toml:"enforce"`
}

// categoriesFile is the embed-relative path of the default list.
const categoriesFile = "substrate/config/categories.toml"

var (
	embeddedCategoriesOnce sync.Once
	embeddedCategories     []string
	embeddedCategoriesErr  error
)

// EmbeddedCategories returns the default category list from the embedded
// substrate file. Parsed once. Empty on parse failure (tests assert it succeeds).
func EmbeddedCategories() []string {
	embeddedCategoriesOnce.Do(func() {
		raw, err := substrateFS.ReadFile(categoriesFile)
		if err != nil {
			embeddedCategoriesErr = err
			return
		}
		var file struct {
			Categories []string `toml:"categories"`
		}
		if _, err := toml.Decode(string(raw), &file); err != nil {
			embeddedCategoriesErr = err
			return
		}
		embeddedCategories = cleanCategoryList(file.Categories)
	})
	return append([]string(nil), embeddedCategories...)
}

// EmbeddedCategoriesErr is non-nil when the embedded file failed to load/parse.
// Exposed for tests; production call sites use the list (empty on failure).
func EmbeddedCategoriesErr() error {
	_ = EmbeddedCategories()
	return embeddedCategoriesErr
}

// ResolveCategoryVocabulary returns the effective allowed category values:
// Vocabulary (if set) else EmbeddedCategories, then Extra, deduped case-insensitively
// (first casing wins). Never a Go-hardcoded list.
func (c Config) ResolveCategoryVocabulary() []string {
	var base []string
	if len(c.Categories.Vocabulary) > 0 {
		base = cleanCategoryList(c.Categories.Vocabulary)
	} else {
		base = EmbeddedCategories()
	}
	extra := cleanCategoryList(c.Categories.Extra)
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, v := range base {
		k := strings.ToLower(v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	for _, v := range extra {
		k := strings.ToLower(v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

// CategoryEnforce returns the effective enforcement phase: off | warn | reject.
// Empty/unknown defaults to warn so an upgrade never silently hard-rejects.
func (c Config) CategoryEnforce() string {
	switch strings.ToLower(strings.TrimSpace(c.Categories.Enforce)) {
	case "off", "false", "0":
		return "off"
	case "reject", "error", "hard":
		return "reject"
	default:
		return "warn"
	}
}

// CanonicaliseCategory validates cat against the resolved vocabulary and
// rewrites a match to the declared casing.
//
// Rules:
//   - Empty cat: pass through (structure still requires non-empty when gated).
//   - EqualFold match: return the declared casing (every enforce mode).
//   - No match + enforce reject: named error listing allowed values.
//   - No match + warn/off: return cat unchanged (CLI surfaces warn separately).
func (c Config) CanonicaliseCategory(cat string) (string, error) {
	cat = strings.TrimSpace(cat)
	if cat == "" {
		return "", nil
	}
	allowed := c.ResolveCategoryVocabulary()
	if len(allowed) == 0 {
		return cat, nil
	}
	if canon, err := matchAllowed(allowed, cat); err == nil {
		return canon, nil
	}
	if c.CategoryEnforce() == "reject" {
		return "", fmt.Errorf("category: unknown %q (want %s)", cat, joinAllowed(allowed))
	}
	return cat, nil
}

// CategoryWarning returns a non-empty advisory when cat is unknown and enforce
// is warn; empty otherwise. Used by the CLI create/set path (stderr, exit 0).
func (c Config) CategoryWarning(cat string) string {
	cat = strings.TrimSpace(cat)
	if cat == "" || c.CategoryEnforce() != "warn" {
		return ""
	}
	allowed := c.ResolveCategoryVocabulary()
	if len(allowed) == 0 {
		return ""
	}
	if _, err := matchAllowed(allowed, cat); err == nil {
		return ""
	}
	return fmt.Sprintf(
		"notice: category %q is not in the controlled vocabulary (want %s).\n"+
			"  add it via [categories] extra = [%q] in .satelle/satelle.toml, or pick a listed value.\n"+
			"  set [categories] enforce = \"off\" to silence; enforce = \"reject\" to hard-fail.\n",
		cat, joinAllowed(allowed), cat,
	)
}

func cleanCategoryList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
