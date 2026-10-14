// Package cloudid is the provider seam for the cloud identity an agent profile
// names (sty_b61d2066, epic:agent-cloud-identity). A Provider adapter owns one
// cloud: the keys its profile table carries, how a session is signed in from the
// machine's [vars] secrets, and what the provider persists as a result. A third
// cloud is a new adapter registered here — neither the launcher nor the config
// loader names a cloud.
//
// satelle SELECTS the identity a session runs as. It does not enforce what that
// identity may do and it cannot isolate a secret from an agent running as the
// same OS user: the cloud's roles and policy, plus a separate OS user, do the
// enforcing. What satelle does guarantee is bounded by each adapter's Prepare:
// the long-lived secret never reaches the agent's environment or argv.
package cloudid

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Field is one key of a provider's profile table.
type Field struct {
	Key      string
	Required bool
	// Secret marks a credential: its value must be exactly one ${NAME} reference
	// into the machine [vars], never a literal.
	Secret bool
}

// Overlay is what Prepare asks of the launched agent's environment: pairs to set
// and names to remove. An unset is not a secret filter — an adapter declares one
// only where the provider's own precedence lets an ambient credential outrank
// the identity the profile selected.
type Overlay struct {
	Set   map[string]string
	Unset []string
}

// Provider is one cloud's adapter.
type Provider interface {
	// Name is the key under a profile's cloud table (azure, aws).
	Name() string
	// Fields is the complete key set of the profile table.
	Fields() []Field
	// Persistence names, for the help text, what the provider keeps on disk or
	// hands the agent as a result of signing in.
	Persistence() string
	// Prepare signs the session in before the agent exists. cred holds the
	// profile table with every ${NAME} expanded; stateDir is the satelle-owned
	// directory for this profile (mode 0700) under which the adapter keeps its
	// provider config. It returns the environment overlay for the agent.
	Prepare(ctx context.Context, profile string, cred map[string]string, stateDir string) (Overlay, error)
}

var providers = map[string]Provider{}

func register(p Provider) { providers[p.Name()] = p }

func init() {
	register(azure{})
	register(aws{})
}

// Lookup returns the adapter registered under name.
func Lookup(name string) (Provider, bool) {
	p, ok := providers[name]
	return p, ok
}

// Providers returns every adapter, sorted by name.
func Providers() []Provider {
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Provider, len(names))
	for i, n := range names {
		out[i] = providers[n]
	}
	return out
}

// credRef is a whole-value ${NAME} reference, the only form a secret field may
// take. The name shape is the upper-case environment-variable convention.
var credRef = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]*\}$`)

// anyRef finds a ${NAME} reference anywhere in a value.
var anyRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// profileName is the shape of a profile name that carries a cloud identity: it
// becomes a directory name and part of an AWS profile name.
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidProfileName reports whether name can carry a cloud identity.
func ValidProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("profile name %q cannot carry a cloud identity — use letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

// Validate checks one provider's profile table: a known provider, only its
// keys, every required key present, no control characters, and every secret
// field exactly one ${NAME} reference. A rejected literal is never echoed.
func Validate(provider string, table map[string]string) error {
	p, ok := Lookup(provider)
	if !ok {
		var names []string
		for _, q := range Providers() {
			names = append(names, q.Name())
		}
		return fmt.Errorf("cloud.%s: unknown provider (want %s)", provider, strings.Join(names, ", "))
	}
	known := map[string]Field{}
	for _, f := range p.Fields() {
		known[f.Key] = f
	}
	var unknown []string
	for k := range table {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("cloud.%s: unknown key(s): %s", provider, strings.Join(unknown, ", "))
	}
	for _, f := range p.Fields() {
		v, present := table[f.Key]
		if !present || strings.TrimSpace(v) == "" {
			if f.Required {
				return fmt.Errorf("cloud.%s: %s is required", provider, f.Key)
			}
			continue
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("cloud.%s.%s: must be a single line", provider, f.Key)
		}
		if f.Secret && !credRef.MatchString(v) {
			return fmt.Errorf("cloud.%s.%s: literal secret — reference a [vars] entry as ${NAME}", provider, f.Key)
		}
	}
	return nil
}

// RefNames returns the [vars] names a table references, sorted and de-duplicated.
func RefNames(table map[string]string) []string {
	seen := map[string]bool{}
	for _, v := range table {
		for _, m := range anyRef.FindAllStringSubmatch(v, -1) {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// PersistenceHelp renders each adapter's Persistence line, one per provider, for
// the launch command's help text.
func PersistenceHelp() string {
	var b strings.Builder
	for _, p := range Providers() {
		fmt.Fprintf(&b, "  %s\n", p.Persistence())
	}
	return b.String()
}
