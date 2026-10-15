// cloudidentity.go — which launch steps the in-loop (driving) session runs
// before its harness starts (sty_b8310408, epic:agent-cloud-identity). ONE
// resolver, InLoopLaunchSteps, answers it for both consumers: `satelle agents
// launch` (which runs them) and the session-start warning (which only needs to
// know steps are declared).
//
// The steps are client configuration: a catalog profile carries a `launch`
// table, a repo selects the profile with `profile =` on its in-loop executor
// seat, and nothing is configured by default. A step's secrets are ${NAME}
// references and resolve from the machine's catalog [vars] and the repo's
// gitignored satelle.local.toml ONLY — never from the committed satelle.toml,
// which is shared and would put a secret's name (or value) in git.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ResolvedStep is one launch step with its references expanded in memory.
type ResolvedStep struct {
	// Run is the argv, with ${SATELLE_PROFILE_DIR} expanded.
	Run []string
	// Env is the step's literal environment.
	Env map[string]string
	// Secrets maps an environment name to the resolved [vars] value. It reaches
	// the step's process and nothing else; never log or print it.
	Secrets map[string]string
}

// LaunchSelection is what a session is to run before its harness starts.
type LaunchSelection struct {
	// Profile is the catalog profile the in-loop executor seat names.
	Profile string
	// Dir is the satelle-owned directory of the profile (ProfileDirVar).
	Dir string
	// Steps run in order.
	Steps []ResolvedStep
	// Set and Unset shape the launched session's environment. Never secret.
	Set   map[string]string
	Unset []string
}

// ProfileDir is the satelle-owned directory a profile's launch steps keep their
// state in: GlobalDir()/profiles/<name>.
func ProfileDir(profile string) string {
	return filepath.Join(GlobalDir(), "profiles", profile)
}

// LaunchVars is the only [vars] source a step's secret resolves from: the
// machine catalog's [vars] under the repo's gitignored satelle.local.toml [vars].
func LaunchVars(global GlobalAgentsConfig, localVars map[string]string) map[string]string {
	return LayerVars(global.Vars, localVars)
}

// LocalVarsAt reads [vars] from the gitignored satelle.local.toml of the repo
// rooted at repoRoot. An absent or unreadable file is nil: the committed
// satelle.toml is deliberately not consulted.
func LocalVarsAt(repoRoot string) map[string]string {
	b, err := os.ReadFile(filepath.Join(repoRoot, DefaultDataDir, LocalConfigName))
	if err != nil {
		return nil
	}
	var doc struct {
		Vars map[string]string `toml:"vars"`
	}
	if _, err := toml.Decode(string(b), &doc); err != nil {
		return nil
	}
	return doc.Vars
}

// InLoopLaunchSteps reports the launch steps the in-loop seat's profile
// declares. It selects only when the effective executor seat is the in-loop
// command AND names a catalog profile (profile =) that declares a launch table,
// directly or through a profile it extends; a seat dispatched as a child never
// selects, because the steps belong to the driving session. profile= does not
// move the seat out of the loop — a binding's own command = "in-loop" wins.
//
// When a secrets reference cannot be resolved the selection is still returned
// (ok true, so the warning can name the profile) together with the error, which
// names the reference and never a value.
func InLoopLaunchSteps(eff EffectiveAgents, global GlobalAgentsConfig, localVars map[string]string) (LaunchSelection, bool, error) {
	seat := eff.Agents.ExecutorBinding()
	if !IsInLoopCommand(seat.Command) {
		return LaunchSelection{}, false, nil
	}
	return ResolveLaunchProfile(global, seat.Profile, localVars)
}

// ResolveLaunchProfile is the profile half of InLoopLaunchSteps: the launch
// table the catalog profile named profile declares, directly or through a
// profile it extends, with its references expanded. ok is false when the name
// is empty or the profile declares nothing to do.
func ResolveLaunchProfile(global GlobalAgentsConfig, profile string, localVars map[string]string) (LaunchSelection, bool, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return LaunchSelection{}, false, nil
	}
	chain, err := global.resolveChain(profile)
	if err != nil {
		return LaunchSelection{}, false, fmt.Errorf("profile %q: %w", profile, err)
	}
	folded, _ := foldChain(global, chain, SourceProfile)
	if !folded.Launch.Declared() {
		return LaunchSelection{}, false, nil
	}
	sel := LaunchSelection{Profile: profile, Dir: ProfileDir(profile)}
	if err := ValidProfileName(profile); err != nil {
		return sel, true, err
	}
	if err := ValidateLaunch(folded.Launch); err != nil {
		return sel, true, err
	}
	secretVars := LaunchVars(global, localVars)
	for _, name := range folded.Launch.RefNames() {
		if _, ok := secretVars[name]; !ok {
			return sel, true, fmt.Errorf("launch: ${%s} is not defined under [vars] in %s or in the gitignored %s — a committed %s is never read for launch secrets",
				name, GlobalAgentsLabel, LocalConfigName, ConfigName)
		}
	}
	// ${SATELLE_PROFILE_DIR} is the only reference outside secrets, supplied
	// through the same expander as every other reference.
	dirVars := map[string]string{ProfileDirVar: sel.Dir}
	plain := func(field, v string) (string, error) {
		out, err := ExpandVars(v, dirVars)
		if err != nil {
			return "", fmt.Errorf("%s: unresolved reference", field)
		}
		return out, nil
	}
	sel.Set = make(map[string]string, len(folded.Launch.Set))
	for k, v := range folded.Launch.Set {
		ev, err := plain("launch.set."+k, v)
		if err != nil {
			return sel, true, err
		}
		sel.Set[k] = ev
	}
	sel.Unset = append([]string(nil), folded.Launch.Unset...)
	for i, step := range folded.Launch.Steps {
		at := fmt.Sprintf("launch.steps[%d]", i)
		rs := ResolvedStep{Run: make([]string, len(step.Run)), Env: make(map[string]string, len(step.Env)), Secrets: make(map[string]string, len(step.Secrets))}
		for j, a := range step.Run {
			ev, err := plain(fmt.Sprintf("%s.run[%d]", at, j), a)
			if err != nil {
				return sel, true, err
			}
			rs.Run[j] = ev
		}
		for k, v := range step.Env {
			ev, err := plain(at+".env."+k, v)
			if err != nil {
				return sel, true, err
			}
			rs.Env[k] = ev
		}
		for k, v := range step.Secrets {
			ev, err := ExpandVars(v, secretVars)
			if err != nil {
				return sel, true, fmt.Errorf("%s.secrets.%s: unresolved reference", at, k)
			}
			rs.Secrets[k] = ev
		}
		sel.Steps = append(sel.Steps, rs)
	}
	return sel, true, nil
}

// LaunchRefProblems reports every ${NAME} a catalog profile's step secrets
// reference that no permitted [vars] source defines: absent everywhere, or
// present only in the committed repo [vars] (which is never read for launch
// secrets). repoVars is the repo's merged [vars]; localVars the gitignored
// overlay alone. Sorted, one line per profile/name.
func LaunchRefProblems(global GlobalAgentsConfig, localVars, repoVars map[string]string) []string {
	vars := LaunchVars(global, localVars)
	var out []string
	for _, name := range sortedKeys(global.Profiles) {
		for _, ref := range global.Profiles[name].Launch.RefNames() {
			if _, ok := vars[ref]; ok {
				continue
			}
			if _, committed := repoVars[ref]; committed {
				out = append(out, fmt.Sprintf("[profiles.%s.launch] ${%s} is defined only in the committed repo [vars] — launch secrets resolve from %s [vars] or the gitignored %s only",
					name, ref, GlobalAgentsLabel, LocalConfigName))
				continue
			}
			out = append(out, fmt.Sprintf("[profiles.%s.launch] ${%s} is not defined under [vars] in %s or in the gitignored %s",
				name, ref, GlobalAgentsLabel, LocalConfigName))
		}
	}
	return out
}

// InLoopEnvWarning is the validate warning for a profile whose env the in-loop
// seat would never apply: the in-loop executor execs no child, so a profile's
// env is inert there (sty_001558ce). The launched session's environment comes
// from launch.set. Empty when the seat is not in-loop, names no profile, or its
// profile has no env.
func InLoopEnvWarning(eff EffectiveAgents, global GlobalAgentsConfig) string {
	seat := eff.Agents.ExecutorBinding()
	profile := strings.TrimSpace(seat.Profile)
	if !IsInLoopCommand(seat.Command) || profile == "" {
		return ""
	}
	chain, err := global.resolveChain(profile)
	if err != nil {
		return ""
	}
	folded, _ := foldChain(global, chain, SourceProfile)
	if len(folded.Env) == 0 {
		return ""
	}
	return fmt.Sprintf("profile %s is the in-loop seat; its env is not applied to the launched session — use launch.set in [profiles.%s.launch]", profile, profile)
}
