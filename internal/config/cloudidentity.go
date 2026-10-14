// cloudidentity.go — which cloud identity the in-loop (driving) session runs as
// (sty_b61d2066, epic:agent-cloud-identity). ONE resolver, InLoopCloudIdentity,
// answers it for both consumers: `satelle agents launch` (which signs in) and
// the session-start warning (which only needs to know an identity is named).
//
// The identity is client configuration: a catalog profile carries a `cloud`
// table, a repo selects the profile with `profile =` on its in-loop executor
// seat, and nothing is configured by default. Credentials are ${NAME}
// references and resolve from the machine's catalog [vars] and the repo's
// gitignored satelle.local.toml ONLY — never from the committed satelle.toml,
// which is shared and would put a secret's name (or value) in git.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/bobmcallan/satelle/internal/cloudid"
)

// CloudSelection is the cloud identity a session is to run as.
type CloudSelection struct {
	// Profile is the catalog profile the in-loop executor seat names.
	Profile string
	// Providers maps a provider (azure | aws) to its profile table with every
	// ${NAME} reference expanded in memory. Empty when Expand failed.
	Providers map[string]map[string]string
}

// ProviderNames returns the selected providers, sorted.
func (s CloudSelection) ProviderNames() []string {
	out := make([]string, 0, len(s.Providers))
	for p := range s.Providers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// CloudVars is the only [vars] source a cloud credential resolves from: the
// machine catalog's [vars] under the repo's gitignored satelle.local.toml [vars].
func CloudVars(global GlobalAgentsConfig, localVars map[string]string) map[string]string {
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

// InLoopCloudIdentity reports the cloud identity the in-loop seat's profile
// names. It selects only when the effective executor seat is the in-loop
// command AND names a catalog profile (profile =) that carries a cloud table,
// directly or through a profile it extends; a seat dispatched as a child never
// selects, because the identity belongs to the driving session. profile= does
// not move the seat out of the loop — a binding's own command = "in-loop" wins.
//
// When a credential reference cannot be resolved the selection is still returned
// (ok true, so the warning can name the profile) together with the error, which
// names the reference and never a value.
func InLoopCloudIdentity(eff EffectiveAgents, global GlobalAgentsConfig, localVars map[string]string) (CloudSelection, bool, error) {
	seat := eff.Agents.ExecutorBinding()
	if !IsInLoopCommand(seat.Command) {
		return CloudSelection{}, false, nil
	}
	return ResolveCloudProfile(global, seat.Profile, localVars)
}

// ResolveCloudProfile is the profile half of InLoopCloudIdentity: the cloud
// identity the catalog profile named profile carries, directly or through a
// profile it extends, with its references expanded. ok is false when the name
// is empty or the profile carries no cloud table. The AWS credential_process
// resolves through this same path, by profile name, at the moment it mints.
func ResolveCloudProfile(global GlobalAgentsConfig, profile string, localVars map[string]string) (CloudSelection, bool, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return CloudSelection{}, false, nil
	}
	chain, err := global.resolveChain(profile)
	if err != nil {
		return CloudSelection{}, false, fmt.Errorf("profile %q: %w", profile, err)
	}
	folded, _ := foldChain(global, chain, SourceProfile)
	if len(folded.Cloud) == 0 {
		return CloudSelection{}, false, nil
	}
	sel := CloudSelection{Profile: profile}
	if err := cloudid.ValidProfileName(profile); err != nil {
		return sel, true, err
	}
	vars := CloudVars(global, localVars)
	providers := make(map[string]map[string]string, len(folded.Cloud))
	for _, p := range sortedCloudProviders(folded.Cloud) {
		table := folded.Cloud[p]
		if err := cloudid.Validate(p, table); err != nil {
			return sel, true, err
		}
		for _, name := range cloudid.RefNames(table) {
			if _, ok := vars[name]; !ok {
				return sel, true, fmt.Errorf("cloud.%s: ${%s} is not defined under [vars] in %s or in the gitignored %s — a committed %s is never read for cloud credentials",
					p, name, GlobalAgentsLabel, LocalConfigName, ConfigName)
			}
		}
		expanded := make(map[string]string, len(table))
		for k, v := range table {
			ev, err := ExpandVars(v, vars)
			if err != nil {
				return sel, true, fmt.Errorf("cloud.%s.%s: unresolved reference", p, k)
			}
			expanded[k] = ev
		}
		providers[p] = expanded
	}
	sel.Providers = providers
	return sel, true, nil
}

func sortedCloudProviders(m map[string]map[string]string) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// CloudRefProblems reports every ${NAME} a catalog profile's cloud identity
// references that no permitted [vars] source defines: absent everywhere, or
// present only in the committed repo [vars] (which is never read for cloud
// credentials). repoVars is the repo's merged [vars]; localVars the gitignored
// overlay alone. Sorted, one line per profile/provider/name.
func CloudRefProblems(global GlobalAgentsConfig, localVars, repoVars map[string]string) []string {
	vars := CloudVars(global, localVars)
	var out []string
	for _, name := range sortedKeys(global.Profiles) {
		cloud := global.Profiles[name].Cloud
		for _, p := range sortedCloudProviders(cloud) {
			for _, ref := range cloudid.RefNames(cloud[p]) {
				if _, ok := vars[ref]; ok {
					continue
				}
				if _, committed := repoVars[ref]; committed {
					out = append(out, fmt.Sprintf("[profiles.%s.cloud.%s] ${%s} is defined only in the committed repo [vars] — cloud credentials resolve from %s [vars] or the gitignored %s only",
						name, p, ref, GlobalAgentsLabel, LocalConfigName))
					continue
				}
				out = append(out, fmt.Sprintf("[profiles.%s.cloud.%s] ${%s} is not defined under [vars] in %s or in the gitignored %s",
					name, p, ref, GlobalAgentsLabel, LocalConfigName))
			}
		}
	}
	return out
}
