// globalagents.go — the machine-wide agent PROFILE catalog at
// $SATELLE_HOME/agents.toml (sty_c7dfeedf / epic:agent-configuration-operability).
//
// The catalog holds named, provider-neutral EXECUTION profiles so an operator
// working across several repositories updates a Claude/Grok/Codex/other
// definition once. It holds NO workflow policy: a profile cannot name a skill,
// a gate, a state, an applies_to, or anything else that decides PROCESS. That
// boundary is mechanical, not advisory — the loader accepts only the known
// binding keys and refuses everything else, naming policy keys specifically.
// Workflows stay repo substrate (see the satelle-repo-agnostic principle and
// the constitution's configuration-over-code rule).
//
// A repo consumes a profile only by naming it: `profile = "<name>"` on a
// binding in .satelle/workflows/agents.toml, or — opt-in — through the catalog's [roles]
// defaults. There is no implicit same-name merge, so adding a profile called
// "reviewer" can never silently change a repo that never asked for it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/bobmcallan/satelle/internal/agentcli"
)

const (
	// GlobalAgentsName is the profile catalog's filename under GlobalDir().
	// It shares agents.toml's basename deliberately — same shape, different
	// scope — and can never be confused with a repo file: the per-repo walk-up
	// only ever looks under .satelle/.
	GlobalAgentsName = "agents.toml"
	// GlobalAgentsLabel is the stable, path-independent name used in validation
	// messages so an error reads the same on every machine (and in tests, where
	// SATELLE_HOME is a temp dir).
	GlobalAgentsLabel = "~/.satelle/agents.toml"
)

// GlobalAgentsConfig is the on-disk shape of the machine-wide catalog.
//
//	[vars]                      # base KV for ${NAME} in profile env/settings;
//	                            # a repo's own [vars] override per key.
//	[profiles.<name>]           # one reusable execution profile (an AgentBinding)
//	[roles]                     # OPT-IN per-role defaults: reviewer = "<profile>"
//
// Every section is optional; an absent file is the zero value and a nil error,
// so a machine that never creates one behaves exactly as before.
type GlobalAgentsConfig struct {
	// Vars is the machine-wide base of the ${NAME} KV. A repo's [vars] (and its
	// gitignored satelle.local.toml overlay) win per key. Secrets belong here on
	// the operator's machine — they are expanded in memory at wiring time and are
	// never written into a repository.
	Vars map[string]string
	// Profiles are the named reusable execution profiles, keyed by profile name.
	Profiles map[string]AgentBinding
	// Roles maps a logical role (reviewer|agent) to a default profile name. It is
	// consulted ONLY for a repo that opts in with [defaults] use_global_roles.
	Roles map[string]string
}

// globalAgentsBindingKeys is the complete set of keys a [profiles.<name>] table
// may carry — the EXECUTION surface of AgentBinding. Anything else is refused at
// load: a policy key with an explanation of the boundary, an unknown key as a
// typo. Deliberately excludes the retired `harness` and `inject_principles`
// aliases: the catalog is new, so it starts on the current field names.
var globalAgentsBindingKeys = map[string]bool{
	"profile":    true, // one profile may extend another
	"role":       true,
	"interface":  true,
	"command":    true,
	"tools":      true,
	"model":      true,
	"effort":     true,
	"timeout":    true,
	"principles": true,
	"env":        true,
	"settings":   true,
	"secondary":  true,
}

// globalAgentsPolicyKeys are keys that would make a profile decide PROCESS
// rather than execution. They are called out by name so the refusal teaches the
// boundary instead of reading as a generic typo.
var globalAgentsPolicyKeys = map[string]bool{
	"applies_to":     true,
	"workflow":       true,
	"workflows":      true,
	"skill":          true,
	"skills":         true,
	"review_skill":   true,
	"reviewer_skill": true,
	"create_review":  true,
	"prompt":         true,
	"gate":           true,
	"gates":          true,
	"states":         true,
	"on":             true,
	"mandatory":      true,
	"parallel":       true,
	"category":       true,
	"categories":     true,
}

// GlobalAgentsPath returns the path to the machine-wide profile catalog.
func GlobalAgentsPath() string {
	return filepath.Join(GlobalDir(), GlobalAgentsName)
}

// LoadGlobalAgents reads the machine-wide profile catalog. An absent file is the
// zero value and a nil error (zero-config machines are unchanged); a present but
// malformed or policy-carrying file is an error, so a broken catalog fails loud
// rather than silently resolving to embedded defaults.
func LoadGlobalAgents() (GlobalAgentsConfig, error) {
	return loadGlobalAgentsFile(GlobalAgentsPath())
}

// loadGlobalAgentsFile is the path-explicit loader (tests and multi-home
// fixtures call it directly; LoadGlobalAgents supplies GlobalAgentsPath).
func loadGlobalAgentsFile(path string) (GlobalAgentsConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GlobalAgentsConfig{}, nil
		}
		return GlobalAgentsConfig{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	g, err := ParseGlobalAgents(string(b))
	if err != nil {
		return GlobalAgentsConfig{}, fmt.Errorf("%s (%s): %w", GlobalAgentsLabel, path, err)
	}
	return g, nil
}

// ParseGlobalAgents decodes and validates a catalog body. Exported so the
// migration path and tests can check content without touching disk.
func ParseGlobalAgents(content string) (GlobalAgentsConfig, error) {
	var raw map[string]toml.Primitive
	md, err := toml.Decode(content, &raw)
	if err != nil {
		return GlobalAgentsConfig{}, err
	}
	g := GlobalAgentsConfig{
		Vars:     map[string]string{},
		Profiles: map[string]AgentBinding{},
		Roles:    map[string]string{},
	}
	for _, key := range sortedPrimitiveKeys(raw) {
		prim := raw[key]
		switch key {
		case "vars":
			if err := md.PrimitiveDecode(prim, &g.Vars); err != nil {
				return GlobalAgentsConfig{}, fmt.Errorf("[vars]: %w", err)
			}
		case "roles":
			if err := md.PrimitiveDecode(prim, &g.Roles); err != nil {
				return GlobalAgentsConfig{}, fmt.Errorf("[roles]: %w", err)
			}
		case "profiles":
			if err := decodeGlobalProfiles(md, prim, g.Profiles); err != nil {
				return GlobalAgentsConfig{}, err
			}
		default:
			// A top-level section other than the three is refused rather than
			// ignored: the catalog's whole contract is that it carries execution
			// configuration only, so an unrecognised section (a stray [workflows],
			// a mistyped [profile]) must not pass silently.
			return GlobalAgentsConfig{}, fmt.Errorf(
				"unknown top-level section [%s] — the catalog holds only [vars], [profiles.<name>], and [roles]; workflows and skills stay repo substrate under .satelle/", key)
		}
	}
	if err := g.validate(); err != nil {
		return GlobalAgentsConfig{}, err
	}
	return g, nil
}

// decodeGlobalProfiles decodes the [profiles] container, checking each profile's
// key set before decoding it into an AgentBinding.
func decodeGlobalProfiles(md toml.MetaData, prim toml.Primitive, into map[string]AgentBinding) error {
	var tables map[string]toml.Primitive
	if err := md.PrimitiveDecode(prim, &tables); err != nil {
		return fmt.Errorf("[profiles]: %w", err)
	}
	for _, name := range sortedPrimitiveKeys(tables) {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("[profiles] carries an unnamed profile")
		}
		var keys map[string]any
		if err := md.PrimitiveDecode(tables[name], &keys); err != nil {
			return fmt.Errorf("[profiles.%s]: %w", name, err)
		}
		if err := checkGlobalProfileKeys(name, keys); err != nil {
			return err
		}
		var b AgentBinding
		if err := md.PrimitiveDecode(tables[name], &b); err != nil {
			return fmt.Errorf("[profiles.%s]: %w", name, err)
		}
		into[name] = b
	}
	return nil
}

// checkGlobalProfileKeys enforces the execution-only boundary mechanically: a
// profile may carry the AgentBinding execution keys and nothing else.
func checkGlobalProfileKeys(name string, keys map[string]any) error {
	var policy, unknown []string
	for k := range keys {
		if globalAgentsBindingKeys[k] {
			continue
		}
		switch {
		case globalAgentsPolicyKeys[k], strings.HasPrefix(k, "output_"), strings.HasPrefix(k, "on_"):
			policy = append(policy, k)
		default:
			unknown = append(unknown, k)
		}
	}
	sort.Strings(policy)
	sort.Strings(unknown)
	if len(policy) > 0 {
		return fmt.Errorf(
			"[profiles.%s] declares workflow policy (%s) — the machine-wide catalog is EXECUTION configuration only; which step runs which skill stays repo substrate under .satelle/workflows",
			name, strings.Join(policy, ", "))
	}
	if len(unknown) > 0 {
		return fmt.Errorf("[profiles.%s] unknown key(s): %s — a profile carries %s",
			name, strings.Join(unknown, ", "), strings.Join(sortedSetKeys(globalAgentsBindingKeys), ", "))
	}
	return nil
}

// validate runs the deterministic catalog checks: valid roles, valid interfaces,
// parseable timeouts, resolvable [roles] targets, and no profile reference cycle.
func (g GlobalAgentsConfig) validate() error {
	for _, name := range sortedKeys(g.Profiles) {
		b := g.Profiles[name]
		section := "profiles." + name
		if r := strings.ToLower(strings.TrimSpace(b.Role)); r != "" && r != RoleReviewer && r != RoleAgent {
			return fmt.Errorf("[%s] role %q: want %q or %q", section, b.Role, RoleReviewer, RoleAgent)
		}
		if err := checkBindingInterface(GlobalAgentsLabel, section, b); err != nil {
			return err
		}
		if err := checkBindingTimeout(GlobalAgentsLabel, section, b); err != nil {
			return err
		}
		if _, err := g.resolveChain(name); err != nil {
			return fmt.Errorf("[%s]: %w", section, err)
		}
	}
	for _, role := range sortedStringKeys(g.Roles) {
		r := strings.ToLower(strings.TrimSpace(role))
		if r != RoleReviewer && r != RoleAgent {
			return fmt.Errorf("[roles] %q: want %q or %q", role, RoleReviewer, RoleAgent)
		}
		target := strings.TrimSpace(g.Roles[role])
		if target == "" {
			return fmt.Errorf("[roles] %s is empty — name a profile or drop the key", role)
		}
		if _, ok := g.Profiles[target]; !ok {
			return fmt.Errorf("[roles] %s = %q names no [profiles.%s]%s", role, target, target, g.knownProfiles())
		}
	}
	return nil
}

// resolveChain returns the profile reference chain starting at name, outermost
// first (name, then what it extends, …). A missing profile or a cycle is an
// error naming the loop, so a broken catalog never resolves half-way.
func (g GlobalAgentsConfig) resolveChain(name string) ([]string, error) {
	var chain []string
	seen := map[string]bool{}
	cur := strings.TrimSpace(name)
	for cur != "" {
		b, ok := g.Profiles[cur]
		if !ok {
			return nil, fmt.Errorf("references missing profile %q%s", cur, g.knownProfiles())
		}
		if seen[cur] {
			return nil, fmt.Errorf("profile reference cycle: %s -> %s", strings.Join(chain, " -> "), cur)
		}
		seen[cur] = true
		chain = append(chain, cur)
		cur = strings.TrimSpace(b.Profile)
	}
	return chain, nil
}

// knownProfiles renders a short " (known: a, b)" hint, or "" when the catalog is
// empty — so a missing-profile error tells the operator what IS defined.
func (g GlobalAgentsConfig) knownProfiles() string {
	names := sortedKeys(g.Profiles)
	if len(names) == 0 {
		return " (the catalog defines no profiles)"
	}
	return " (known: " + strings.Join(names, ", ") + ")"
}

// checkBindingInterface is the shared interface check used by BOTH the repo
// loader and the catalog loader, so the two files can never disagree about what
// a valid interface= is. file is the label the error names.
func checkBindingInterface(file, section string, b AgentBinding) error {
	raw := strings.TrimSpace(b.Interface)
	if raw == "" {
		return nil
	}
	switch strings.ToLower(raw) {
	case InterfaceCommand, InterfaceACP:
		return nil
	default:
		return fmt.Errorf("%s [%s] interface %q: want %q or %q",
			file, section, raw, InterfaceCommand, InterfaceACP)
	}
}

// checkBindingTimeout is the shared timeout check (see checkBindingInterface).
func checkBindingTimeout(file, section string, b AgentBinding) error {
	if _, err := b.TimeoutDuration(0); err != nil {
		return fmt.Errorf("%s [%s] timeout: %w", file, section, err)
	}
	return nil
}

// StarterGlobalAgents renders a documented starter catalog derived from the
// machine's selected agent CLI (`~/.satelle/config.toml [agent] cli`) — the
// backward-compatible migration path for an installation that predates the
// catalog (AC6). It is CONTENT ONLY: no repo is touched, no repo value is read,
// and no secret is embedded. Secrets stay in [vars] on this machine and reach a
// profile only as a ${NAME} reference, expanded in memory at dispatch wiring.
//
// The generated catalog changes nothing on its own: a repo picks it up only by
// writing `profile = "<name>"`.
func StarterGlobalAgents(cli string) (string, error) {
	runner, err := agentcli.NewRunner(cli)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(starterGlobalAgentsTemplate, cli, cli, runner.Command(), DefaultReviewerTools, cli), nil
}

const starterGlobalAgentsTemplate = `# satelle machine-wide agent PROFILE catalog (~/.satelle/agents.toml).
#
# Reusable EXECUTION configuration shared by every repository on this machine:
# update a provider definition here once instead of in each repo. It carries no
# workflow policy — which step runs which skill stays repo substrate under
# .satelle/workflows, and a profile that names a skill, gate, state, or
# applies_to is REFUSED at load.
#
# Nothing here applies to a repo until that repo asks for it:
#
#   # .satelle/workflows/agents.toml
#   [reviewer]
#   profile = "%s-reviewer"   # explicit reference; repo keys below still win
#   effort  = "low"
#
# Precedence, highest first: repo inline value -> the referenced profile ->
# an opt-in [roles] default ([defaults] use_global_roles = true in the repo) ->
# satelle's embedded fallback. A profile that merely shares a binding's NAME is
# never merged in — there is no implicit same-name inheritance.

[vars]
# Machine-wide KV for ${NAME} references in a profile's env/settings. A repo's
# own [vars] (and its gitignored satelle.local.toml) win per key. Secrets live
# here, on this machine: they are expanded in memory at dispatch and are never
# written into a repository.
# GLM_API_KEY = "sk-..."

[profiles.%s-reviewer]
role       = "reviewer"
interface  = "command"
command    = %q
tools      = %q
principles = "session"
# model   = "..."     # per-profile model
# effort  = "low"     # reasoning effort
# timeout = "20m"     # dispatch bound
# env     = { ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}" }

[roles]
# OPT-IN per-role defaults. These reach ONLY a repo that sets
# [defaults] use_global_roles = true in its own .satelle/workflows/agents.toml, and only
# for a binding that names no profile= of its own. Leave commented out to keep
# every repo explicit.
# reviewer = "%s-reviewer"
`

func sortedPrimitiveKeys(m map[string]toml.Primitive) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
