package config

// Machine-scope keys that used to live in a per-repo satelle.toml / satelle.local.toml
// and are now owned by ~/.satelle/config.toml (sty_21a7d16d). A leftover assignment
// is never silently ignored: MachineScopeStrays reports it and Stray.Warning is
// the one renderer (doctor, status, migrate plan, settings-reject).

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Stray is one machine-scope key still present in a repo config file.
type Stray struct {
	Key            string // web_port | [server] endpoint | [service] <name>
	File           string // satelle.toml or satelle.local.toml (basename)
	RepoValue      string // raw right-hand side as written in the repo file
	EffectiveValue string // value satelled actually uses
}

// Warning is the single AC4 renderer: names the key, the value satelled used,
// and the command that re-homes it. Settings-reject uses this exact sentence.
func (s Stray) Warning() string {
	file := s.File
	if file == "" {
		file = ConfigName
	}
	return "stray machine-scope key " + s.Key + " in .satelle/" + file +
		" (repo value " + s.RepoValue + "); satelled uses " + s.EffectiveValue +
		". Re-home with `satelle migrate --yes`."
}

// MachineScopeStrays raw-byte-scans satelle.toml and satelle.local.toml under
// dataDir for uncommented web_port, [server] endpoint, and any [service] key.
// It does not decode those keys into Config — they have no repo-scope fields.
func MachineScopeStrays(dataDir string) []Stray {
	if strings.TrimSpace(dataDir) == "" {
		return nil
	}
	gc, _ := LoadGlobal()
	var out []Stray
	for _, name := range []string{ConfigName, LocalConfigName} {
		path := filepath.Join(dataDir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, scanMachineScopeStrays(string(b), name, gc)...)
	}
	return out
}

func scanMachineScopeStrays(content, file string, gc GlobalConfig) []Stray {
	lines := strings.Split(content, "\n")
	var out []Stray
	section := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isTableHeader(line) {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		key, val, ok := assignmentKV(line)
		if !ok {
			continue
		}
		switch {
		case section == "" && key == "web_port":
			out = append(out, Stray{
				Key:            "web_port",
				File:           file,
				RepoValue:      val,
				EffectiveValue: strconv.Itoa(gc.Service.ResolvePort()),
			})
		case section == "server" && key == "endpoint":
			eff := gc.Service.ResolveEndpoint()
			if eff == "" {
				eff = "(disabled)"
			}
			out = append(out, Stray{
				Key:            "[server] endpoint",
				File:           file,
				RepoValue:      val,
				EffectiveValue: eff,
			})
		case section == "service":
			out = append(out, Stray{
				Key:            "[service] " + key,
				File:           file,
				RepoValue:      val,
				EffectiveValue: serviceEffective(gc, key),
			})
		}
	}
	return out
}

func serviceEffective(gc GlobalConfig, key string) string {
	switch key {
	case "port":
		return strconv.Itoa(gc.Service.ResolvePort())
	case "addr":
		return gc.Service.ResolveAddr()
	case "endpoint":
		if e := gc.Service.ResolveEndpoint(); e != "" {
			return e
		}
		return "(disabled)"
	case "repo":
		if r := strings.TrimSpace(gc.Service.Repo); r != "" {
			return r
		}
		return "(unset)"
	default:
		return "(ignored)"
	}
}

// assignmentKV parses an uncommented `key = value` line. The value is the
// trimmed right-hand side with surrounding quotes stripped.
func assignmentKV(line string) (key, val string, ok bool) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	if key == "" || strings.ContainsAny(key, " \t[]") {
		return "", "", false
	}
	val = strings.TrimSpace(line[eq+1:])
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			if u, err := strconv.Unquote(val); err == nil {
				val = u
			} else {
				val = val[1 : len(val)-1]
			}
		}
	}
	return key, val, true
}
