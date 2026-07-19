package web

// Per-project settings page (sty_e1740d82): a READ-ONLY view of the repo's
// committed .satelle/satelle.toml. Repo config is edited by changing that file
// directly (and committing it under the workflow) or via `satelle settings`
// (sty_e2fba595), NOT through this page — it carries no form and no write path.
// The only EDITABLE settings in the UI are the machine-wide ones on the global
// settings page (globalsettings.go), which owns the loopback+CSRF write gate.
// The repo-key schema and value display live in internal/config (config.Settings /
// config.SettingDisplay), shared with the CLI so the two surfaces never drift.

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

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
	// MirrorRO hides the global-settings link (no write surface on push-fed serve).
	MirrorRO bool
}

func configPathFor(a *app.App) string {
	return filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName)
}

// settingsGet renders the read-only table from a FRESH config load, so an on-disk
// edit to satelle.toml (via the file or `satelle settings`, the only mutation
// paths now) shows on reload without restarting serve.
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
	for _, s := range config.Settings {
		vm := settingsRowVM{FieldID: s.FieldID(), Label: s.Label, Help: s.Help, Value: config.SettingDisplay(cfg, s)}
		if s.Section != lastSect {
			vm.SectHead = sectionLabel(s.Section)
			lastSect = s.Section
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
