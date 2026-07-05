package web

// Per-project settings page (sty_e1740d82): a READ-ONLY view of the repo's
// committed .satelle/satelle.toml. Repo config is edited by changing that file
// directly (and committing it under the workflow), NOT via the web UI — so this
// page carries no form and no write path. The only EDITABLE settings in the UI are
// the machine-wide ones on the global settings page (globalsettings.go), which owns
// the loopback+CSRF write gate. Viewing is unauthenticated like every other page
// (satelle.toml is committed anyway). hosted.server is intentionally absent here —
// it is machine-wide now and lives on the global settings page.

import (
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// settingRow declares one repo config key rendered read-only. Section "" = a root key.
type settingRow struct {
	Section string
	Key     string
	Label   string
	Help    string
}

// settingsSchema is the list of repo keys shown on the read-only page, in render
// order. hosted.server is deliberately NOT here — it moved to global settings.
var settingsSchema = []settingRow{
	{"hosted", "project", "Hosted project", "Project slug this repo maps to."},
	{"", "web_port", "Web port", "Applies on the next serve start."},
	{"", "log_level", "Log level", "debug | info | warn | error"},
	{"", "data_dir", "Data dir", "Applies on the next serve start."},
	{"", "db", "Database path", "Applies on the next serve start."},
	{"", "substrate_roots", "Substrate roots", "Authored-kind → parent dir overrides."},
	{"", "logs_max_size_kb", "Log rotation size (KB)", "Per evidence log before it rolls."},
	{"", "logs_max_files", "Log files kept", "Rotations retained."},
	{"", "stories_keep_closed", "Keep closed stories (count)", "0 = no count pruning."},
	{"", "stories_keep_days", "Keep closed stories (days)", "0 = no age pruning."},
	{"review", "gate_create", "Gate create", "Structure-review story/task on creation."},
	{"gate", "edit_exempt_paths", "Edit-gate exempt paths", "Path prefixes exempt from the engaged-story edit gate."},
}

func (r settingRow) fieldID() string {
	if r.Section == "" {
		return r.Key
	}
	return r.Section + "." + r.Key
}

// settingsRowVM is one rendered read-only row for the template.
type settingsRowVM struct {
	FieldID  string // the config key, shown as the row's monospace id
	Label    string
	Help     string
	Value    string
	SectHead string // non-empty on the first row of a new section group
}

type settingsData struct {
	Rows     []settingsRowVM
	TopBar   topBar
	RepoRoot string
}

func configPathFor(a *app.App) string {
	return filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName)
}

// settingsGet renders the read-only table from a FRESH config load, so an on-disk
// edit to satelle.toml (the only mutation path now) shows on reload without
// restarting serve.
func settingsGet(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, _, _ := config.Load(configPathFor(a))
		render(w, "settings", settingsData{
			Rows:     settingsRows(cfg),
			TopBar:   newTopBar(""),
			RepoRoot: a.RepoRoot,
		})
	}
}

func settingsRows(cfg config.Config) []settingsRowVM {
	var rows []settingsRowVM
	lastSect := "\x00"
	for _, row := range settingsSchema {
		vm := settingsRowVM{FieldID: row.fieldID(), Label: row.Label, Help: row.Help, Value: settingDisplay(cfg, row)}
		if row.Section != lastSect {
			vm.SectHead = sectionLabel(row.Section)
			lastSect = row.Section
		}
		rows = append(rows, vm)
	}
	return rows
}

func sectionLabel(s string) string {
	if s == "" {
		return "General"
	}
	return strings.ToUpper(s[:1]) + s[1:] // "hosted" → "Hosted", "gate" → "Gate"
}

func settingDisplay(cfg config.Config, row settingRow) string {
	switch row.fieldID() {
	case "hosted.project":
		return cfg.Hosted.Project
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
	}
	return ""
}

// substrateRootsDisplay renders the kind→dir map one "kind = dir" per line, sorted
// so the read-only view is deterministic.
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
