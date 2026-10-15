// launch.go — the pre-launch steps a catalog profile declares (sty_b8310408,
// epic:agent-cloud-identity). satelle knows no cloud, no CLI and no sign-in
// command: a profile lists commands to run, environment variables to set and
// environment variables to clear, and `satelle agents launch` runs them before
// it starts the harness. What the commands do is the operator's configuration.
//
// The reference rule is the whole safety story. A `${NAME}` that names a
// [vars] secret is allowed in exactly one place — a step's `secrets` value,
// which reaches that step's process environment and nothing else. `run`, `env`
// and `set` may reference only the reserved ${SATELLE_PROFILE_DIR}, so no
// secret can land in a step's argv or in the launched session's environment.
package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ProfileDirVar is the reserved reference to the satelle-owned directory of the
// launched profile (GlobalDir()/profiles/<name>). It is supplied through the same
// ExpandVars vars map as any other reference — there is no second expander.
const ProfileDirVar = "SATELLE_PROFILE_DIR"

// LaunchSpec is `[profiles.<name>.launch]`.
type LaunchSpec struct {
	// Set is the environment the launched session receives. Values are literals
	// or contain ${SATELLE_PROFILE_DIR}; never a [vars] reference.
	Set map[string]string `toml:"set"`
	// Unset names environment variables removed from the launched session.
	Unset []string `toml:"unset"`
	// Steps run in order, before the harness starts.
	Steps []LaunchStep `toml:"steps"`
}

// LaunchStep is one `[[profiles.<name>.launch.steps]]` entry.
type LaunchStep struct {
	// Run is the command and its arguments (argv, never a shell string).
	Run []string `toml:"run"`
	// Env is extra environment for this step; literals or ${SATELLE_PROFILE_DIR}.
	Env map[string]string `toml:"env"`
	// Secrets maps an environment name to exactly one ${NAME} [vars] reference.
	// The value reaches this step's process environment only.
	Secrets map[string]string `toml:"secrets"`
}

// Declared reports whether the spec asks the launcher to do anything.
func (s *LaunchSpec) Declared() bool {
	return s != nil && (len(s.Steps) > 0 || len(s.Set) > 0 || len(s.Unset) > 0)
}

// credRef is a whole-value ${NAME} reference, the only form a secrets value may
// take. The name shape is the upper-case environment-variable convention.
var credRef = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]*\}$`)

// anyRef finds a ${NAME} reference anywhere in a value.
var anyRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// envName is the shape of an environment variable name.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// profileName is the shape of a profile name that carries launch steps: it
// becomes a directory name.
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidProfileName reports whether name can carry launch steps.
func ValidProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("profile name %q cannot carry launch steps — use letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

// ValidateLaunch checks one profile's launch table. Every message names the
// field and key (or a [vars] entry's name) and never a value, so a rejected
// literal secret is not echoed.
func ValidateLaunch(spec *LaunchSpec) error {
	if spec == nil {
		return nil
	}
	if err := checkEnvMap("launch.set", spec.Set); err != nil {
		return err
	}
	for i, k := range spec.Unset {
		if !envName.MatchString(k) {
			return fmt.Errorf("launch.unset[%d]: not an environment variable name", i)
		}
	}
	for i, step := range spec.Steps {
		at := fmt.Sprintf("launch.steps[%d]", i)
		if len(step.Run) == 0 || strings.TrimSpace(step.Run[0]) == "" {
			return fmt.Errorf("%s.run: a command is required", at)
		}
		for j, a := range step.Run {
			if err := checkPlain(fmt.Sprintf("%s.run[%d]", at, j), a); err != nil {
				return err
			}
		}
		if err := checkEnvMap(at+".env", step.Env); err != nil {
			return err
		}
		for _, k := range sortedStringKeys(step.Secrets) {
			field := at + ".secrets." + k
			if !envName.MatchString(k) {
				return fmt.Errorf("%s: not an environment variable name", field)
			}
			v := step.Secrets[k]
			if strings.ContainsAny(v, "\r\n\x00") {
				return fmt.Errorf("%s: must be a single line", field)
			}
			if !credRef.MatchString(v) {
				return fmt.Errorf("%s: literal secret — reference a [vars] entry as exactly one ${NAME}", field)
			}
			if name := v[2 : len(v)-1]; name == ProfileDirVar || name == ScratchEnv {
				return fmt.Errorf("%s: %s is reserved and is not a [vars] entry", field, name)
			}
			if _, dup := step.Env[k]; dup {
				return fmt.Errorf("%s: also set under %s.env — name it in one place", field, at)
			}
		}
	}
	return nil
}

// checkEnvMap validates an env-shaped table whose values may not reference a
// [vars] entry.
func checkEnvMap(field string, m map[string]string) error {
	for _, k := range sortedStringKeys(m) {
		if !envName.MatchString(k) {
			return fmt.Errorf("%s.%s: not an environment variable name", field, k)
		}
		if err := checkPlain(field+"."+k, m[k]); err != nil {
			return err
		}
	}
	return nil
}

// checkPlain enforces the reference rule on a value outside a step's secrets:
// a single line that references nothing but the reserved profile directory.
func checkPlain(field, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s: must be a single line", field)
	}
	for _, m := range anyRef.FindAllStringSubmatch(v, -1) {
		if m[1] != ProfileDirVar {
			return fmt.Errorf("%s: references %s, which is not %s — only a step's secrets may reference a [vars] entry", field, m[1], ProfileDirVar)
		}
	}
	return nil
}

// RefNames returns the [vars] names a spec's step secrets reference, sorted and
// de-duplicated. Only secrets can reference [vars]; ValidateLaunch enforces it.
func (s *LaunchSpec) RefNames() []string {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, step := range s.Steps {
		for _, v := range step.Secrets {
			for _, m := range anyRef.FindAllStringSubmatch(v, -1) {
				seen[m[1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// LegacyCloudProblem is the load error for a catalog that still carries the
// 0.0.672 `[profiles.<name>.cloud]` table, which is no longer read.
func LegacyCloudProblem(section string) error {
	return fmt.Errorf("[%s.cloud] is the 0.0.672 identity table and is no longer read — declare [%s.launch] steps instead (satelle help global-agents)", section, section)
}
