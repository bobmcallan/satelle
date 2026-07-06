package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// varRef matches a ${NAME} reference in an env value: a leading letter or
// underscore, then word characters — the conventional environment-variable name
// shape. Only this exact ${...} form is a KV reference; a bare $FOO, a bare
// {model}, or a ${ with no closing brace passes through verbatim. ${...} is
// expanded ONLY in env VALUES (never in a harness argv template), so it can never
// collide with the {system}/{tools}/{model} argv placeholders, which buildArgs
// matches as whole bare-brace tokens (sty_001558ce).
var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandVars replaces every ${NAME} in s with vars[NAME]. An unknown NAME is an
// error naming the missing var — never a silent empty, so a mistyped or absent
// key fails loudly rather than dispatching an agent with a blank credential.
// There is no escape syntax: a literal ${...} has no use case in an env value here.
func ExpandVars(s string, vars map[string]string) (string, error) {
	var missing []string
	out := varRef.ReplaceAllStringFunc(s, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unknown var ${%s} — define it under [vars] in %s (secrets) or %s",
			strings.Join(dedupeSorted(missing), "}, ${"), LocalConfigName, ConfigName)
	}
	return out, nil
}

// ResolveAgentEnvs returns a copy of the agents layer with every binding's Env
// values ${NAME}-expanded against vars (executor, reviewer, and each named
// agent). Resolution happens ONCE, at CLI wiring time (fail-fast, the repo's
// established style): a broken ${VAR} in any binding refuses the command with an
// actionable message rather than sitting undetected until a dispatch. A binding
// with no Env is a no-op, so a zero-config repo stays zero-config. The error
// names the binding section and env key but NEVER the value (values may be
// secrets — env is kept out of all evidence, logs, and errors).
func ResolveAgentEnvs(a AgentsConfig, vars map[string]string) (AgentsConfig, error) {
	res := a
	rex, err := resolveBindingEnv("executor", a.Executor, vars)
	if err != nil {
		return AgentsConfig{}, err
	}
	res.Executor = rex
	rrv, err := resolveBindingEnv("reviewer", a.Reviewer, vars)
	if err != nil {
		return AgentsConfig{}, err
	}
	res.Reviewer = rrv
	if len(a.Agents) > 0 {
		res.Agents = make(map[string]AgentBinding, len(a.Agents))
		for _, name := range sortedKeys(a.Agents) {
			rb, err := resolveBindingEnv(name, a.Agents[name], vars)
			if err != nil {
				return AgentsConfig{}, err
			}
			res.Agents[name] = rb
		}
	}
	return res, nil
}

// resolveBindingEnv returns b with its Env values expanded. section is the
// agents.toml section name ([executor]/[reviewer]/[<name>]) used only in the error.
func resolveBindingEnv(section string, b AgentBinding, vars map[string]string) (AgentBinding, error) {
	if len(b.Env) == 0 {
		return b, nil
	}
	resolved := make(map[string]string, len(b.Env))
	for _, k := range sortedStringKeys(b.Env) {
		v, err := ExpandVars(b.Env[k], vars)
		if err != nil {
			return AgentBinding{}, fmt.Errorf("agents.toml [%s] env %q: %w", section, k, err)
		}
		resolved[k] = v
	}
	b.Env = resolved
	return b, nil
}

func dedupeSorted(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]AgentBinding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
