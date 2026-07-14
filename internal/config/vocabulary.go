package config

// Controlled tag-namespace vocabulary (sty_034d843c / epic:surface-scoped-steps).
//
// A repo may declare that certain tag namespaces (e.g. surface) only accept a
// fixed set of values. The vocabulary is REPO CONFIG in satelle.toml — the
// binary never hardcodes which namespaces or values exist. Empty/absent means
// every tag is free-form (zero-config and every existing test keep passing).

import (
	"fmt"
	"sort"
	"strings"
)

// TagsConfig holds the optional controlled-vocabulary table for work-item tags.
// Declared in satelle.toml as:
//
//	[tags.vocabulary]
//	surface = ["ui", "cli"]
//
// A tag whose namespace is listed must use a declared value (matched
// case-insensitively, stored in the casing declared here). Namespaces absent
// from the table stay free-form.
type TagsConfig struct {
	// Vocabulary maps a tag namespace (without trailing colon) to the allowed
	// values for that namespace. Empty map / unset = no controlled namespaces.
	Vocabulary map[string][]string `toml:"vocabulary"`
}

// CanonicaliseTags validates tags against the configured vocabulary and
// rewrites matching tags to the casing declared in config.
//
// Rules:
//   - No colon, or namespace not in Vocabulary → pass through unchanged.
//   - Namespace present: value must EqualFold-match an allowed value; emit
//     namespace:declaredValue (canonical casing from config).
//   - No match: named error listing the allowed values (ParseScope-style).
//   - Empty/absent Vocabulary: return tags unchanged, nil error.
//
// The word for any particular namespace (e.g. surface) never appears here —
// only the generic mechanism. Values and namespaces come from config alone.
func (c Config) CanonicaliseTags(tags []string) ([]string, error) {
	vocab := c.Tags.Vocabulary
	if len(vocab) == 0 {
		return tags, nil
	}
	// Index namespaces case-insensitively so [tags.vocabulary] Surface=… still
	// matches a surface:ui tag (do not repeat the plain-== trap).
	type entry struct {
		ns      string // as declared in config (canonical ns casing)
		allowed []string
	}
	byNS := make(map[string]entry, len(vocab))
	for ns, allowed := range vocab {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		clean := make([]string, 0, len(allowed))
		for _, v := range allowed {
			v = strings.TrimSpace(v)
			if v != "" {
				clean = append(clean, v)
			}
		}
		if len(clean) == 0 {
			continue
		}
		byNS[strings.ToLower(ns)] = entry{ns: ns, allowed: clean}
	}
	if len(byNS) == 0 {
		return tags, nil
	}

	out := make([]string, len(tags))
	for i, tag := range tags {
		ns, val, ok := splitTag(tag)
		if !ok {
			out[i] = tag
			continue
		}
		e, controlled := byNS[strings.ToLower(ns)]
		if !controlled {
			out[i] = tag
			continue
		}
		canonical, err := matchAllowed(e.allowed, val)
		if err != nil {
			return nil, fmt.Errorf("tags: unknown %s %q (want %s)", e.ns, val, joinAllowed(e.allowed))
		}
		// Store with the namespace casing declared in config + the matched value
		// as declared (canonical casing), so exact-equality matching stays correct.
		out[i] = e.ns + ":" + canonical
	}
	return out, nil
}

// splitTag splits "namespace:value" on the first colon. A tag with no colon
// (or empty value side) returns ok=false and is left free-form.
func splitTag(tag string) (ns, val string, ok bool) {
	i := strings.IndexByte(tag, ':')
	if i <= 0 || i == len(tag)-1 {
		return "", "", false
	}
	return tag[:i], tag[i+1:], true
}

// matchAllowed returns the allowed value that EqualFold-matches val, or an error.
func matchAllowed(allowed []string, val string) (string, error) {
	for _, a := range allowed {
		if strings.EqualFold(a, val) {
			return a, nil
		}
	}
	return "", fmt.Errorf("no match")
}

// joinAllowed renders allowed values as "a|b|c" for error messages.
func joinAllowed(allowed []string) string {
	cp := append([]string(nil), allowed...)
	sort.Strings(cp)
	return strings.Join(cp, "|")
}
