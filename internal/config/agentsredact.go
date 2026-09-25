// agentsredact.go — redaction is a property of agents-kind TRANSPORT
// (sty_01949949, architecture review R1). Every path that moves an agents
// layer off this machine or onto it goes through RedactAgentsTransport:
//
//   - `satelle sync config push` (redactForTransmit, area "agents"),
//   - `satelle publish push` when the resolved kind is "agents",
//   - `satelle sync bindings push`,
//   - `satelle sync bindings pull` on INGEST, before the workspace layer lands.
//
// Owning it here, once, is what makes the security property a property of the
// entity rather than of whichever command the operator happened to type. The
// ingest call is deliberate: a catalog entry produced by an older, unredacted
// path must not land absolute paths or env values as live bindings.
//
// Redaction is SUBTRACTIVE and the result must still load: env KEYS survive
// (they are the useful half of the contract — "this binding needs
// ANTHROPIC_AUTH_TOKEN"); a value that is a pure `${VAR}` REFERENCE survives
// too, because a variable NAME is not a secret and it is exactly what lets the
// receiving machine's [vars] fail-fast fire when the value is missing; every
// LITERAL value is blanked. Command tokens that are absolute paths are reduced
// to their base name so the receiving machine's PATH is the resolver; profile=
// (a machine-wide catalog name) and the retired harness= alias are dropped.
//
// A blank left by redaction means "declared, unsatisfied". The resolver treats
// a workspace-tier blank exactly that way — it is dropped from the merge, never
// laid over the machine's real environment (resolveagents.go).
package config

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// varRefPattern matches a value that is exactly one ${NAME} reference.
var varRefPattern = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// IsVarRef reports whether v is a pure ${NAME} reference — a variable name,
// not a value, and therefore safe to transport.
func IsVarRef(v string) bool {
	return varRefPattern.MatchString(strings.TrimSpace(v))
}

// redactValue blanks a literal and keeps a pure ${VAR} reference.
func redactValue(v string) string {
	if IsVarRef(v) {
		return strings.TrimSpace(v)
	}
	return ""
}

// RedactAgentsTransport decodes an agents-layer body, strips everything
// machine-local or secret, and re-encodes it in the flat form LoadAgents reads.
// A body that does not decode is an ERROR — it is never passed through raw.
func RedactAgentsTransport(body []byte) ([]byte, error) {
	ac, err := decodeAgents(string(body), false)
	if err != nil {
		return nil, fmt.Errorf("redact agents layer: %w", err)
	}
	return EncodeAgents(RedactAgents(ac))
}

// RedactAgents returns a deep copy of a with every binding redacted for
// transport. The Defaults table carries no secrets and is kept.
func RedactAgents(a AgentsConfig) AgentsConfig {
	out := AgentsConfig{Defaults: a.Defaults, Executor: redactBinding(a.Executor), Reviewer: redactBinding(a.Reviewer)}
	if len(a.Agents) > 0 {
		out.Agents = make(map[string]AgentBinding, len(a.Agents))
		for name, b := range a.Agents {
			out.Agents[name] = redactBinding(b)
		}
	}
	return out
}

func redactBinding(b AgentBinding) AgentBinding {
	out := b
	out.Command = redactCommand(b.CommandTemplate())
	out.Harness = ""
	out.Profile = ""
	if len(b.Env) > 0 {
		out.Env = make(map[string]string, len(b.Env))
		for k, v := range b.Env {
			out.Env[k] = redactValue(v)
		}
	}
	if len(b.Settings) > 0 {
		out.Settings = redactSettings(b.Settings, false)
	}
	return out
}

// redactCommand reduces absolute-path tokens to their base name. Placeholders
// and relative tokens pass through unchanged.
func redactCommand(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if filepath.IsAbs(f) {
			fields[i] = filepath.Base(f)
		}
	}
	return strings.Join(fields, " ")
}

// secretishKey reports whether a settings key names a credential-shaped value.
func secretishKey(k string) bool {
	l := strings.ToLower(k)
	for _, marker := range []string{"token", "secret", "password", "apikey", "api_key", "api-key"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return l == "key" || strings.HasSuffix(l, "_key") || strings.HasSuffix(l, "-key")
}

// redactSettings walks the settings tree. Under an `env` sub-table every value
// is blanked; elsewhere secret-shaped keys are blanked; absolute-path strings
// (anywhere, including list entries such as permissions.additionalDirectories)
// are reduced to their base name; nested tables and lists recurse.
func redactSettings(m map[string]any, underEnv bool) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		switch child := v.(type) {
		case map[string]any:
			out[k] = redactSettings(child, underEnv || lk == "env")
		case []any:
			out[k] = redactList(child)
		default:
			if underEnv || secretishKey(k) {
				if s, ok := v.(string); ok {
					out[k] = redactValue(s)
				} else {
					out[k] = ""
				}
				continue
			}
			out[k] = redactScalar(v)
		}
	}
	return out
}

// PruneUnsatisfied drops what redaction left blank — env keys with "" values
// and blank secret/settings.env leaves — so a transported layer contributes
// only what it actually carries. Used on the workspace tier before it is
// merged: a blank there is "declared, unsatisfied", and must never shadow the
// machine's own environment or settings.local.json (sty_01949949 AC3).
func PruneUnsatisfied(b AgentBinding) AgentBinding {
	out := b
	if len(b.Env) > 0 {
		env := make(map[string]string, len(b.Env))
		for k, v := range b.Env {
			if strings.TrimSpace(v) != "" {
				env[k] = v
			}
		}
		out.Env = env
		if len(env) == 0 {
			out.Env = nil
		}
	}
	if len(b.Settings) > 0 {
		out.Settings = pruneBlankSettings(b.Settings)
		if len(out.Settings) == 0 {
			out.Settings = nil
		}
	}
	return out
}

func pruneBlankSettings(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch child := v.(type) {
		case map[string]any:
			if p := pruneBlankSettings(child); len(p) > 0 {
				out[k] = p
			}
		case string:
			if strings.TrimSpace(child) != "" {
				out[k] = child
			}
		default:
			out[k] = v
		}
	}
	return out
}

func redactList(l []any) []any {
	out := make([]any, len(l))
	for i, v := range l {
		switch child := v.(type) {
		case map[string]any:
			out[i] = redactSettings(child, false)
		case []any:
			out[i] = redactList(child)
		default:
			out[i] = redactScalar(v)
		}
	}
	return out
}

func redactScalar(v any) any {
	if s, ok := v.(string); ok && filepath.IsAbs(s) {
		return filepath.Base(s)
	}
	return v
}

// RehydrateAgents is the INGEST counterpart on the personal config-sync path
// (`satelle sync config deploy`, sty_01949949 code-ac review). The store copy
// is redacted, so materialising it over the authored file would blank live
// env values, drop profile= and strip command paths — silent corruption of a
// working repo. Given the store body and the on-disk authored body:
//
//   - keepLocal=true when the local file redacts to exactly the store body:
//     the store holds nothing the repo does not already have, so the authored
//     bytes (comments and all) are left untouched;
//   - otherwise the store's layout wins (that is what deploy means) and, per
//     binding, everything redaction removed is re-applied from the local file:
//     blank env values, blank secret settings, base-named command tokens whose
//     local counterpart is the absolute path, and profile=.
//
// A local file that does not parse cannot be merged; the caller decides.
func RehydrateAgents(store, local []byte) (out []byte, keepLocal bool, err error) {
	if len(bytes.TrimSpace(local)) == 0 {
		return store, false, nil
	}
	redactedLocal, err := RedactAgentsTransport(local)
	if err != nil {
		return nil, false, err
	}
	if bytes.Equal(bytes.TrimSpace(redactedLocal), bytes.TrimSpace(store)) {
		return nil, true, nil
	}
	sc, err := decodeAgents(string(store), false)
	if err != nil {
		return nil, false, fmt.Errorf("store agents layer: %w", err)
	}
	lc, err := decodeAgents(string(local), false)
	if err != nil {
		return nil, false, fmt.Errorf("local agents layer: %w", err)
	}
	merged := AgentsConfig{
		Defaults: sc.Defaults,
		Executor: rehydrateBinding(sc.Executor, lc.Executor),
		Reviewer: rehydrateBinding(sc.Reviewer, lc.Reviewer),
	}
	if len(sc.Agents) > 0 {
		merged.Agents = make(map[string]AgentBinding, len(sc.Agents))
		for name, b := range sc.Agents {
			merged.Agents[name] = rehydrateBinding(b, lc.Agents[name])
		}
	}
	out, err = EncodeAgents(merged)
	return out, false, err
}

func rehydrateBinding(s, l AgentBinding) AgentBinding {
	out := s
	if len(s.Env) > 0 && len(l.Env) > 0 {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			if v == "" {
				if lv, ok := l.Env[k]; ok {
					v = lv
				}
			}
			out.Env[k] = v
		}
	}
	sf := strings.Fields(s.Command)
	lf := strings.Fields(l.CommandTemplate())
	if len(sf) > 0 && len(lf) > 0 && sf[0] != lf[0] && filepath.IsAbs(lf[0]) && filepath.Base(lf[0]) == sf[0] {
		sf[0] = lf[0]
		out.Command = strings.Join(sf, " ")
	}
	if strings.TrimSpace(s.Profile) == "" && strings.TrimSpace(l.Profile) != "" {
		out.Profile = l.Profile
	}
	if len(s.Settings) > 0 && len(l.Settings) > 0 {
		out.Settings = rehydrateSettings(s.Settings, l.Settings)
	}
	return out
}

func rehydrateSettings(s, l map[string]any) map[string]any {
	out := make(map[string]any, len(s))
	for k, v := range s {
		lv, has := l[k]
		switch child := v.(type) {
		case map[string]any:
			if lm, ok := lv.(map[string]any); ok && has {
				out[k] = rehydrateSettings(child, lm)
			} else {
				out[k] = child
			}
		case []any:
			if ll, ok := lv.([]any); ok && has {
				out[k] = rehydrateList(child, ll)
			} else {
				out[k] = child
			}
		default:
			out[k] = rehydrateScalar(v, lv)
		}
	}
	return out
}

func rehydrateList(s, l []any) []any {
	out := make([]any, len(s))
	for i, v := range s {
		var lv any
		if i < len(l) {
			lv = l[i]
		}
		switch child := v.(type) {
		case map[string]any:
			if lm, ok := lv.(map[string]any); ok {
				out[i] = rehydrateSettings(child, lm)
			} else {
				out[i] = child
			}
		case []any:
			if ll, ok := lv.([]any); ok {
				out[i] = rehydrateList(child, ll)
			} else {
				out[i] = child
			}
		default:
			out[i] = rehydrateScalar(v, lv)
		}
	}
	return out
}

// rehydrateScalar restores a redacted scalar from its local counterpart: a
// blank string takes the local value; a base name takes the local absolute
// path with that base. Anything else keeps the store's value.
func rehydrateScalar(s, l any) any {
	ss, ok := s.(string)
	if !ok {
		return s
	}
	ls, ok := l.(string)
	if !ok {
		return s
	}
	if ss == "" {
		return ls
	}
	if filepath.IsAbs(ls) && filepath.Base(ls) == ss {
		return ls
	}
	return s
}

// EncodeAgents marshals an agents layer to TOML in the flat form LoadAgents
// reads: [defaults] when set, [executor], [reviewer], then every named binding
// as a top-level table (never the retired nested [agents.<name>]). Only
// non-empty fields are written, so a round trip through decodeAgents is
// lossless for everything the loader keeps. Tables and keys are emitted sorted,
// so the bytes are deterministic and a re-push of unchanged config is a no-op.
func EncodeAgents(a AgentsConfig) ([]byte, error) {
	tables := map[string]map[string]any{}
	if d := defaultsTable(a.Defaults); len(d) > 0 {
		tables["defaults"] = d
	}
	if t := bindingTable(a.Executor); len(t) > 0 {
		tables["executor"] = t
	}
	if t := bindingTable(a.Reviewer); len(t) > 0 {
		tables["reviewer"] = t
	}
	for name, b := range a.Agents {
		switch name {
		case "defaults", "models", "executor", "reviewer", "agents":
			continue // cannot be a named binding in the flat form
		}
		t := bindingTable(b)
		if len(t) == 0 {
			t = map[string]any{} // an empty named table still declares the name
		}
		tables[name] = t
	}
	// One Encode over the whole document, so nested env/settings tables are
	// written as [<name>.env] under their binding rather than as stray
	// top-level tables. The encoder sorts keys, so the bytes are deterministic.
	doc := make(map[string]any, len(tables))
	var empty []string
	for n, t := range tables {
		if len(t) == 0 {
			empty = append(empty, n) // the encoder elides an empty table; declare it by hand
			continue
		}
		doc[n] = t
	}
	var buf bytes.Buffer
	if len(doc) > 0 {
		if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
			return nil, fmt.Errorf("encode agents layer: %w", err)
		}
	}
	sort.Strings(empty)
	for _, n := range empty {
		if buf.Len() > 0 && !bytes.HasSuffix(buf.Bytes(), []byte("\n\n")) {
			buf.WriteString("\n")
		}
		fmt.Fprintf(&buf, "[%s]\n", n)
	}
	return buf.Bytes(), nil
}

func defaultsTable(d AgentsDefaults) map[string]any {
	out := map[string]any{}
	if s := strings.TrimSpace(d.Secondary); s != "" {
		out["secondary"] = s
	}
	if d.UseGlobalRoles {
		out["use_global_roles"] = true
	}
	return out
}

func bindingTable(b AgentBinding) map[string]any {
	out := map[string]any{}
	set := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	set("interface", b.Interface)
	set("command", b.Command)
	set("tools", b.Tools)
	set("model", b.Model)
	set("role", b.Role)
	set("principles", b.Principles)
	set("timeout", b.Timeout)
	set("idle_timeout", b.IdleTimeout)
	set("busy_timeout", b.BusyTimeout)
	set("effort", b.Effort)
	set("secondary", b.Secondary)
	set("profile", b.Profile)
	if b.InjectPrinciples != nil {
		out["inject_principles"] = *b.InjectPrinciples
	}
	if len(b.Env) > 0 {
		env := make(map[string]string, len(b.Env))
		for k, v := range b.Env {
			env[k] = v
		}
		out["env"] = env
	}
	if len(b.Settings) > 0 {
		out["settings"] = b.Settings
	}
	return out
}
