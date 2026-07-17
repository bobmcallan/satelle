package config

// Repo-settings SCHEMA (sty_e2fba595): the single source of truth for the committed
// satelle.toml keys the UI and CLI expose. Both the read-only web settings page
// (internal/web, sty_e1740d82) and the `satelle settings` CLI iterate THIS list, so
// the two surfaces never drift. hosted.server is deliberately absent — it is the
// machine-wide global binding (~/.satelle/config.toml), not a repo key.
//
// A Setting carries how to DISPLAY a resolved value (SettingDisplay) and how to
// ENCODE a user-supplied value into a TOML right-hand side for the surgical
// SaveConfigValues upsert (EncodeValue). substrate_roots is display-only: a
// kind→dir map has no clean single-argument CLI encoding, so it is edited by
// changing satelle.toml directly.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// settingKind classifies a repo key so a value can be validated and rendered.
type settingKind int

const (
	kindString settingKind = iota // free text → quoted
	kindInt                       // whole number → bare digits
	kindBool                      // true|false
	kindEnum                      // one of a fixed set → quoted
	kindList                      // comma-separated → ["a", "b"]
	kindMap                       // display-only (no CLI encoding)
)

// Setting declares one committed-config key. Section "" is a root key (above the
// first table header). Enum holds the allowed values for kindEnum.
type Setting struct {
	Section string
	Key     string
	Label   string
	Help    string
	Kind    settingKind
	Enum    []string
}

// Settings is the ordered repo-key whitelist. The order and Label/Help strings are
// shared with the web settings page, so it renders identically.
var Settings = []Setting{
	{Key: "web_port", Label: "Web port", Help: "Applies on the next serve start.", Kind: kindInt},
	{Key: "log_level", Label: "Log level", Help: "debug | info | warn | error", Kind: kindEnum, Enum: []string{"debug", "info", "warn", "error"}},
	{Key: "data_dir", Label: "Data dir", Help: "Authored substrate home under the repo (default .satelle). Runtime state is home-keyed separately. Applies on the next serve start.", Kind: kindString},
	{Key: "db", Label: "Database path", Help: "Override for the per-repo local ledger; default is ~/.satelle/<repo-key>/satelle.db (one project per DB). Applies on the next serve start.", Kind: kindString},
	{Key: "substrate_roots", Label: "Substrate roots", Help: "Authored-kind → parent dir overrides.", Kind: kindMap},
	{Key: "logs_max_size_kb", Label: "Log rotation size (KB)", Help: "Per evidence log before it rolls.", Kind: kindInt},
	{Key: "logs_max_files", Label: "Log files kept", Help: "Rotations retained.", Kind: kindInt},
	{Key: "stories_keep_closed", Label: "Keep closed stories (count)", Help: "0 = no count pruning.", Kind: kindInt},
	{Key: "stories_keep_days", Label: "Keep closed stories (days)", Help: "0 = no age pruning.", Kind: kindInt},
	{Section: "review", Key: "gate_create", Label: "Gate create", Help: "Run structure + create_review on story/task create (default on at init).", Kind: kindBool},
	{Section: "gate", Key: "edit_exempt_paths", Label: "Edit-gate exempt paths", Help: "Path prefixes exempt from the engaged-story edit gate. Init seeds .satelle/ (authored substrate) and .gitignore (satelle-managed output).", Kind: kindList},
	{Section: "gate", Key: "allow_parallel", Label: "Allow parallel stories", Help: "Opt out of one-performing-story enforcement. Does not implement worktrees/merge — only disables the blocker.", Kind: kindBool},
	{Section: "gate", Key: "allow_outside_tree_edits", Label: "Allow outside-tree edits", Help: "Opt in to Bash/Edit mutations in another repo's working tree. Non-repo paths are never fenced. Default deny; only for a deliberate multi-repo install.", Kind: kindBool},
	{Section: "hosted", Key: "project", Label: "Hosted project", Help: "Project slug this repo maps to (personal sync target).", Kind: kindString},
	{Section: "hosted", Key: "workspace", Label: "Active workspace", Help: "Scoped-sync destination — personal default; a team-workspace name elects it.", Kind: kindString},
}

// FieldID is the dotted key path ("section.key", or the bare key for a root key).
func (s Setting) FieldID() string {
	if s.Section == "" {
		return s.Key
	}
	return s.Section + "." + s.Key
}

// SettingByID looks up a Setting by its FieldID ("review.gate_create") OR its bare
// Key ("gate_create") — every bare key in the schema is unique across sections, so
// the short form is unambiguous and accepted for convenience.
func SettingByID(id string) (Setting, bool) {
	for _, s := range Settings {
		if s.FieldID() == id || s.Key == id {
			return s, true
		}
	}
	return Setting{}, false
}

// SettingIDs returns every settable repo key's FieldID, in schema order (for help
// and unknown-key errors).
func SettingIDs() []string {
	ids := make([]string, len(Settings))
	for i, s := range Settings {
		ids[i] = s.FieldID()
	}
	return ids
}

// SettingDisplay returns the resolved value of a key as a display string (empty for
// an unset value; a bool renders "true"/"false"; a list/map renders one item per
// line). It is the single display entry point shared by the web page and the CLI.
func SettingDisplay(cfg Config, s Setting) string {
	switch s.FieldID() {
	case "web_port":
		return intStr(cfg.WebPort)
	case "log_level":
		return cfg.LogLevel
	case "data_dir":
		return cfg.DataDir
	case "db":
		return cfg.DB
	case "substrate_roots":
		return substrateRootsDisplay(cfg.SubstrateRoots)
	case "logs_max_size_kb":
		return intStr(cfg.LogsMaxSizeKB)
	case "logs_max_files":
		return intStr(cfg.LogsMaxFiles)
	case "stories_keep_closed":
		return intStr(cfg.StoriesKeepClosed)
	case "stories_keep_days":
		return intStr(cfg.StoriesKeepDays)
	case "review.gate_create":
		return boolStr(cfg.Review.GateCreate)
	case "gate.edit_exempt_paths":
		return strings.Join(cfg.Gate.EditExemptPaths, "\n")
	case "gate.allow_parallel":
		return boolStr(cfg.Gate.AllowParallel)
	case "gate.allow_outside_tree_edits":
		return boolStr(cfg.Gate.AllowOutsideTreeEdits)
	case "hosted.project":
		return cfg.Hosted.Project
	case "hosted.workspace":
		// The resolved active workspace — "personal" by default (zero-config),
		// else the elected team-workspace name. The overlay is already merged by
		// Load, so this reflects the effective per-developer choice.
		return cfg.ResolveActiveWorkspace().Name
	}
	return ""
}

// EncodeValue validates a user-supplied value for this key and renders the TOML
// right-hand side for a KeyEdit (e.g. `"info"`, `8787`, `true`, `["a", "b"]`).
// Returns an error for an invalid value or a display-only key.
func (s Setting) EncodeValue(input string) (string, error) {
	v := strings.TrimSpace(input)
	switch s.Kind {
	case kindInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("%s: must be a whole number", s.FieldID())
		}
		return strconv.Itoa(n), nil
	case kindBool:
		switch strings.ToLower(v) {
		case "true":
			return "true", nil
		case "false":
			return "false", nil
		}
		return "", fmt.Errorf("%s: must be true or false", s.FieldID())
	case kindEnum:
		for _, opt := range s.Enum {
			if v == opt {
				return strconv.Quote(v), nil
			}
		}
		return "", fmt.Errorf("%s: must be one of %s", s.FieldID(), strings.Join(s.Enum, " | "))
	case kindList:
		return listTOML(splitList(v)), nil
	case kindMap:
		return "", fmt.Errorf("%s is read-only via the CLI — edit .satelle/satelle.toml directly", s.FieldID())
	default: // kindString
		return strconv.Quote(v), nil
	}
}

// substrateRootsDisplay renders the kind→dir map one "kind = dir" per line, sorted
// so the view is deterministic.
func substrateRootsDisplay(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(m))
	for k := range m {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	lines := make([]string, len(kinds))
	for i, k := range kinds {
		lines[i] = k + " = " + m[k]
	}
	return strings.Join(lines, "\n")
}

func intStr(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func listTOML(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
