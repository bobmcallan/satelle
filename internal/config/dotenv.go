package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DotEnvFile is the data-dir-relative path of the developer's secrets file. It
// is configuration like any .env: it is read only by `satelle secret push` and
// never travels as substrate.
const DotEnvFile = "config/.env"

// dotEnvKey is the shape of a key: the conventional environment-variable name.
var dotEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DotEnvPath is where ReadDotEnv looks for dataDir's secrets file.
func DotEnvPath(dataDir string) string {
	return filepath.Join(dataDir, filepath.FromSlash(DotEnvFile))
}

// ReadDotEnv reads KEY=VALUE pairs from dataDir/config/.env. Blank lines and
// whole-line # comments are skipped, an `export ` prefix is accepted, a value may
// be double-quoted (with \n, \r, \t, \" and \\ escapes), single-quoted (literal)
// or bare (trimmed, with a trailing ` #` comment dropped). A missing file is an
// empty map, not an error. A malformed line is an error naming the line number
// only — a value, which may be a secret, is never put in an error.
//
// The .env file is the source of a secret declared `from = "env"` and feeds
// `satelle secret push` and nothing else; a binding's ${VAR} expands from
// [vars] (ResolveAgentEnvs), never from this file.
func ReadDotEnv(dataDir string) (map[string]string, error) {
	f, err := os.Open(DotEnvPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", DotEnvFile, err)
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, raw, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !dotEnvKey.MatchString(key) {
			return nil, fmt.Errorf("%s line %d: expected KEY=VALUE", DotEnvFile, n)
		}
		val, err := parseDotEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", DotEnvFile, n, err)
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", DotEnvFile, err)
	}
	return out, nil
}

func parseDotEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '\'':
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated single quote")
		}
		return raw[1 : 1+end], nil
	case '"':
		var b strings.Builder
		for i := 1; i < len(raw); i++ {
			switch c := raw[i]; c {
			case '"':
				return b.String(), nil
			case '\\':
				i++
				if i >= len(raw) {
					return "", fmt.Errorf("unterminated double quote")
				}
				switch raw[i] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(raw[i])
				}
			default:
				b.WriteByte(c)
			}
		}
		return "", fmt.Errorf("unterminated double quote")
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw), nil
}
