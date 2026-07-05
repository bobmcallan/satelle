package web

// Web settings editor (sty_ffe53865): a key/value table of the repo config,
// written back to the committed satelle.toml with a per-key surgical upsert
// (config.SaveConfigValues), so comments and unmodeled keys survive. VIEWING is
// unauthenticated like every other page (satelle.toml is committed anyway); only
// the WRITE is gated — accepted solely from loopback (127.0.0.1/::1), judged from
// BOTH the child socket and the supervisor-forwarded true client address, plus a
// custom header a cross-origin page cannot set — so the 0.0.0.0-bound view never
// becomes an unauthenticated LAN (or CSRF) write vector.

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// settingsCSRFHeader must be present on a write; a cross-origin page cannot set a
// custom header without a CORS preflight this server never grants.
const settingsCSRFHeader = "X-Satelle-Settings"

type settingType int

const (
	typeString settingType = iota
	typeInt
	typeBool
	typeList
)

// settingRow declares one editable config key. Section "" = a root key.
type settingRow struct {
	Section string
	Key     string
	Label   string
	Type    settingType
	Help    string
}

// settingsSchema is the WHITELIST of writable keys — the POST never writes a key
// outside this list.
var settingsSchema = []settingRow{
	{"hosted", "server", "Hosted server", typeString, "Base URL used by web/CLI sign-in."},
	{"hosted", "project", "Hosted project", typeString, "Project slug this repo maps to."},
	{"", "web_port", "Web port", typeInt, "Applies on the next serve start."},
	{"", "log_level", "Log level", typeString, "debug | info | warn | error"},
	{"", "data_dir", "Data dir", typeString, "Applies on the next serve start."},
	{"", "db", "Database path", typeString, "Applies on the next serve start."},
	{"", "logs_max_size_kb", "Log rotation size (KB)", typeInt, "Per evidence log before it rolls."},
	{"", "logs_max_files", "Log files kept", typeInt, "Rotations retained."},
	{"", "stories_keep_closed", "Keep closed stories (count)", typeInt, "0 = no count pruning."},
	{"", "stories_keep_days", "Keep closed stories (days)", typeInt, "0 = no age pruning."},
	{"review", "gate_create", "Gate create", typeBool, "Structure-review story/task on creation."},
	{"gate", "edit_exempt_paths", "Edit-gate exempt paths", typeList, "One path prefix per line."},
}

func (r settingRow) fieldID() string {
	if r.Section == "" {
		return r.Key
	}
	return r.Section + "." + r.Key
}

// settingsRowVM is one rendered row for the template.
type settingsRowVM struct {
	settingRow
	FieldID  string // exported for the template (form field name)
	Value    string
	IsBool   bool
	IsList   bool
	Checked  bool
	SectHead string // non-empty on the first row of a new section group
}

type settingsData struct {
	Rows     []settingsRowVM
	TopBar   topBar
	RepoRoot string
	Saved    bool
}

func configPathFor(a *app.App) string {
	return filepath.Join(a.RepoRoot, config.DefaultDataDir, config.ConfigName)
}

// settingsGet renders the settings table from a FRESH config load, so a prior
// save shows on reload without restarting serve.
func settingsGet(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, _, _ := config.Load(configPathFor(a))
		render(w, "settings", settingsData{
			Rows:     settingsRows(cfg),
			TopBar:   newTopBar(""),
			RepoRoot: a.RepoRoot,
			Saved:    r.URL.Query().Get("saved") == "1",
		})
	}
}

func settingsRows(cfg config.Config) []settingsRowVM {
	var rows []settingsRowVM
	lastSect := "\x00"
	for _, row := range settingsSchema {
		vm := settingsRowVM{settingRow: row, FieldID: row.fieldID(), Value: settingDisplay(cfg, row)}
		switch row.Type {
		case typeBool:
			vm.IsBool = true
			vm.Checked = vm.Value == "true"
		case typeList:
			vm.IsList = true
		}
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

// settingsPost validates the loopback+CSRF gate, writes only CHANGED keys, and
// rebinds the live sign-in server when [hosted] changes.
func settingsPost(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !settingsWriteAllowed(r) {
			http.Error(w, "config writes are accepted only from this machine (loopback) via the settings page", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		cfg, _, _ := config.Load(configPathFor(a))

		var edits []config.KeyEdit
		hostedChanged := false
		for _, row := range settingsSchema {
			cur := settingDisplay(cfg, row)
			next, rendered, ok, err := settingSubmission(row, r)
			if err != nil {
				http.Error(w, fmt.Sprintf("%s: %v", row.Label, err), http.StatusBadRequest)
				return
			}
			if !ok || next == cur {
				continue // absent or unchanged → leave the file untouched
			}
			edits = append(edits, config.KeyEdit{Section: row.Section, Key: row.Key, Value: rendered})
			if row.Section == "hosted" {
				hostedChanged = true
			}
		}
		if len(edits) > 0 {
			if err := config.SaveConfigValues(configPathFor(a), edits); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if hostedChanged {
			// Keep the live sign-in binding in step, resolved global-first
			// (sty_53ccf845): editing the repo hosted.server key only moves the live
			// binding when no global server is set (the global one wins otherwise).
			ncfg, _, _ := config.Load(configPathFor(a))
			setHostedServer(config.ResolveHostedServer(ncfg))
		}
		http.Redirect(w, r, baseHref()+"settings?saved=1", http.StatusSeeOther)
	}
}

// settingSubmission normalizes a row's submitted value: the display-form string
// (for change detection), the rendered TOML RHS, and whether the field was
// present. Bool is always present (checkbox).
func settingSubmission(row settingRow, r *http.Request) (display, rendered string, ok bool, err error) {
	field := row.fieldID()
	// A checkbox is meaningful by presence (absent = unchecked = false); every
	// other field must be PRESENT in the form to count as submitted, so a partial
	// POST never blanks the keys it did not send.
	_, present := r.Form[field]
	switch row.Type {
	case typeBool:
		checked := r.FormValue(field) != ""
		return boolStr(checked), boolStr(checked), true, nil
	case typeInt:
		if !present {
			return "", "", false, nil
		}
		raw := strings.TrimSpace(r.FormValue(field))
		if raw == "" {
			return "", "", false, nil
		}
		n, cerr := strconv.Atoi(raw)
		if cerr != nil {
			return "", "", false, fmt.Errorf("must be a whole number")
		}
		return strconv.Itoa(n), strconv.Itoa(n), true, nil
	case typeList:
		if !present {
			return "", "", false, nil
		}
		items := splitList(r.FormValue(field))
		return strings.Join(items, "\n"), listTOML(items), true, nil
	default: // string
		if !present {
			return "", "", false, nil
		}
		raw := strings.TrimSpace(r.FormValue(field))
		return raw, strconv.Quote(raw), true, nil
	}
}

// settingsWriteAllowed is the write gate: the CSRF header, a loopback child
// socket, and (when the supervisor forwarded it) a loopback true client address.
func settingsWriteAllowed(r *http.Request) bool {
	if r.Header.Get(settingsCSRFHeader) != "1" {
		return false
	}
	if !addrIsLoopback(r.RemoteAddr) {
		return false
	}
	if ca := r.Header.Get(ClientAddrHeader); ca != "" && !addrIsLoopback(ca) {
		return false
	}
	return true
}

func addrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func settingDisplay(cfg config.Config, row settingRow) string {
	switch row.fieldID() {
	case "hosted.server":
		return cfg.Hosted.Server
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
