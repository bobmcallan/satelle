package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Subscribed command-policy lists (sty_041bf275). A repo opts in to a list of
// [[command_rule]] entries published elsewhere by declaring
// [[gate.command_policy]]. satelle ships no list: with no subscription nothing
// is fetched and no published rule applies.

// DefaultCommandPolicyMaxAge is how long a fetched list stays current when its
// subscription declares no max_age.
const DefaultCommandPolicyMaxAge = 24 * time.Hour

// CommandPolicy is one subscription. Exactly one source is set: a hosted
// publish-catalog path (Workspace and Path, on the configured hosted server) or
// a third-party URL. MaxAge is a Go duration ("12h"); empty means
// DefaultCommandPolicyMaxAge.
type CommandPolicy struct {
	Name      string `toml:"name"`
	Workspace string `toml:"workspace"`
	Path      string `toml:"path"`
	URL       string `toml:"url"`
	MaxAge    string `toml:"max_age"`
}

// IsCatalog reports whether the subscription reads the hosted publish catalog.
func (p CommandPolicy) IsCatalog() bool { return strings.TrimSpace(p.URL) == "" }

// Source is the subscription's source as one display string.
func (p CommandPolicy) Source() string {
	if !p.IsCatalog() {
		return strings.TrimSpace(p.URL)
	}
	return strings.TrimSpace(p.Workspace) + ":" + strings.TrimSpace(p.Path)
}

// Age is the freshness bound: MaxAge, or the default. Load has refused a bad one.
func (p CommandPolicy) Age() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(p.MaxAge)); err == nil && d > 0 {
		return d
	}
	return DefaultCommandPolicyMaxAge
}

// CommandPolicyError is a [[gate.command_policy]] that load refused. Like
// CommandRuleError it names the file, the index and the field, never a value.
// Index is -1 when the problem is a key no field decodes.
type CommandPolicyError struct {
	Path   string
	Index  int
	Field  string
	Reason string
}

func (e *CommandPolicyError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("config: %s: [[gate.command_policy]] key %s: %s", e.Path, e.Field, e.Reason)
	}
	return fmt.Sprintf("config: %s: [[gate.command_policy]] subscription %d field %s: %s", e.Path, e.Index, e.Field, e.Reason)
}

// ValidateGateCommandPolicies refuses a malformed [[gate.command_policy]] with a
// *CommandPolicyError. It makes no network call: whether the list parses is
// `satelle policy refresh`'s check, made on the fetched body.
func ValidateGateCommandPolicies(c Config, path string) error {
	bad := func(i int, field, reason string) error {
		return &CommandPolicyError{Path: path, Index: i, Field: field, Reason: reason}
	}
	seen := map[string]bool{}
	for i, p := range c.Gate.CommandPolicies {
		name := strings.TrimSpace(p.Name)
		switch {
		case name == "":
			return bad(i, "name", "must not be empty")
		case strings.ContainsAny(name, "\r\n"):
			return bad(i, "name", "must be one line")
		case seen[name]:
			return bad(i, "name", "must be unique among subscriptions")
		}
		seen[name] = true
		ws, pth, u := strings.TrimSpace(p.Workspace), strings.TrimSpace(p.Path), strings.TrimSpace(p.URL)
		switch {
		case u != "" && (ws != "" || pth != ""):
			return bad(i, "url", "set either url or workspace and path, not both")
		case u == "" && ws == "" && pth == "":
			return bad(i, "url", "set a source: url, or workspace and path")
		case u == "" && ws == "":
			return bad(i, "workspace", "must be set with path")
		case u == "" && pth == "":
			return bad(i, "path", "must be set with workspace")
		}
		if u != "" {
			if err := validatePolicyURL(u); err != nil {
				return bad(i, "url", err.Error())
			}
		}
		if m := strings.TrimSpace(p.MaxAge); m != "" {
			if d, err := time.ParseDuration(m); err != nil || d <= 0 {
				return bad(i, "max_age", `must be a positive duration such as "12h"`)
			}
		}
	}
	return nil
}

// validatePolicyURL requires https, because a list fetched over a URL carries no
// checksum and plain http would let anyone on the path rewrite the rules. A
// loopback host may use http, so a list served locally can be tried.
func validatePolicyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("must be an absolute https URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	return errors.New("must be an https URL (http only for a loopback host)")
}

// undecodedCommandPolicyKeys refuses a key under [[gate.command_policy]] that no
// field decodes, so a misspelled "urll", or a "sha256" this build does not read,
// cannot silently leave a subscription with no source or no check.
func undecodedCommandPolicyKeys(md toml.MetaData, path string) error {
	for _, k := range md.Undecoded() {
		if len(k) >= 3 && k[0] == "gate" && k[1] == "command_policy" {
			return &CommandPolicyError{Path: path, Index: -1, Field: strings.Join(k[2:], "."), Reason: "unknown key"}
		}
	}
	return nil
}

// ParseCommandPolicyList parses a fetched list: TOML holding [[command_rule]]
// entries, under the same grammar as [[gate.command_rule]]. The rules come back
// compiled. A list that holds no rule, an unknown key, or one bad rule is
// refused whole, so a partial list is never accepted.
func ParseCommandPolicyList(body []byte) ([]CommandRule, error) {
	var doc struct {
		CommandRules []CommandRule `toml:"command_rule"`
	}
	md, err := toml.Decode(string(body), &doc)
	if err != nil {
		return nil, fmt.Errorf("not TOML: %v", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("unknown key %s: a list holds only [[command_rule]] entries", u[0])
	}
	if len(doc.CommandRules) == 0 {
		return nil, errors.New("holds no [[command_rule]] entries")
	}
	c := Config{Gate: GateConfig{CommandRules: doc.CommandRules}}
	if err := ValidateGateCommandRules(c, ""); err != nil {
		var cre *CommandRuleError
		if errors.As(err, &cre) {
			return nil, fmt.Errorf("[[command_rule]] %d field %s: %s", cre.Index, cre.Field, cre.Reason)
		}
		return nil, err
	}
	return doc.CommandRules, nil
}
