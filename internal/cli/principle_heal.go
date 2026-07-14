// principle_heal.go — deterministic principle frontmatter migrations on init
// (sty_cc8ce91c). Removes inert scope: and rewrites principles:always →
// principles:session so binary-introduced drift does not block the stamp.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// residencyAlwaysTag is the retired residency marker rewritten to sessionTag.
const residencyAlwaysTag = "principles:always"

// healPrincipleFrontmatter walks dataDir/principles and applies safe,
// deterministic frontmatter heals. Each mutated file is backed up first.
// Returns human-readable report lines (empty when nothing changed).
func healPrincipleFrontmatter(dataDir string, opts BackupOpts) []string {
	var lines []string
	for _, path := range principleFiles(dataDir) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body := string(raw)
		healed, notes := migratePrincipleFrontmatter(body)
		if len(notes) == 0 {
			continue
		}
		rel := "principles/" + filepath.Base(path)
		if _, berr := backupExistingFile(dataDir, BackupKindPreMutation, rel, raw, opts); berr != nil {
			lines = append(lines, fmt.Sprintf("! %s (heal skipped: backup failed: %v)", rel, berr))
			continue
		}
		if werr := os.WriteFile(path, []byte(healed), 0o644); werr != nil {
			lines = append(lines, fmt.Sprintf("! %s (heal write failed: %v)", rel, werr))
			continue
		}
		lines = append(lines, fmt.Sprintf("~ %s (migrated: %s)", rel, strings.Join(notes, "; ")))
	}
	return lines
}

// migratePrincipleFrontmatter returns the healed body and migration notes.
// Only inert scope: and the always→session residency tag are touched; the
// authored description and body prose are never rewritten.
func migratePrincipleFrontmatter(body string) (healed string, notes []string) {
	fm := frontmatter(body)
	if fm == "" {
		return body, nil
	}
	fmLines := strings.Split(fm, "\n")
	hasSession := principleFMHasTag(fmLines, sessionTag)

	var out []string
	var removedScope bool
	var rewrittenAlways bool

	for i := 0; i < len(fmLines); i++ {
		ln := fmLines[i]
		t := strings.TrimSpace(ln)

		// Drop inert scope: key (value on the same line).
		if t == "scope:" || strings.HasPrefix(t, "scope: ") || strings.HasPrefix(t, "scope:\t") {
			removedScope = true
			continue
		}

		if strings.HasPrefix(t, "tags:") {
			rest := strings.TrimSpace(strings.TrimPrefix(t, "tags:"))
			if strings.HasPrefix(rest, "[") {
				rewritten, changed, nowSession := rewriteAlwaysInInlineTags(rest, hasSession)
				if changed {
					rewrittenAlways = true
					hasSession = nowSession
				}
				indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
				out = append(out, indent+"tags: "+rewritten)
				continue
			}
			// Block list form: emit tags: then process following "- item" lines.
			out = append(out, ln)
			for i+1 < len(fmLines) {
				next := fmLines[i+1]
				n2 := strings.TrimSpace(next)
				if n2 == "" {
					out = append(out, next)
					i++
					continue
				}
				if !strings.HasPrefix(n2, "- ") {
					break
				}
				i++
				item := strings.Trim(strings.TrimSpace(n2[2:]), `"'`)
				if item == residencyAlwaysTag {
					rewrittenAlways = true
					if hasSession {
						continue // drop duplicate always when session already present
					}
					hasSession = true
					indent := next[:len(next)-len(strings.TrimLeft(next, " \t"))]
					rawItem := strings.TrimSpace(n2[2:])
					q := ""
					if strings.HasPrefix(rawItem, "'") || strings.HasPrefix(rawItem, `"`) {
						q = string(rawItem[0])
					}
					if q != "" {
						out = append(out, indent+"- "+q+sessionTag+q)
					} else {
						out = append(out, indent+"- "+sessionTag)
					}
					continue
				}
				out = append(out, next)
			}
			continue
		}
		out = append(out, ln)
	}

	if removedScope {
		notes = append(notes, "removed inert scope:")
	}
	if rewrittenAlways {
		notes = append(notes, residencyAlwaysTag+" → "+sessionTag)
	}
	if len(notes) == 0 {
		return body, nil
	}
	return replaceFrontmatter(body, strings.Join(out, "\n")), notes
}

func principleFMHasTag(fmLines []string, want string) bool {
	for i := 0; i < len(fmLines); i++ {
		t := strings.TrimSpace(fmLines[i])
		if strings.HasPrefix(t, "tags:") {
			rest := strings.TrimSpace(strings.TrimPrefix(t, "tags:"))
			if strings.HasPrefix(rest, "[") {
				for _, tag := range splitTrimTags(strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")) {
					if tag == want {
						return true
					}
				}
				continue
			}
			for j := i + 1; j < len(fmLines); j++ {
				n2 := strings.TrimSpace(fmLines[j])
				if n2 == "" {
					continue
				}
				if !strings.HasPrefix(n2, "- ") {
					break
				}
				item := strings.Trim(strings.TrimSpace(n2[2:]), `"'`)
				if item == want {
					return true
				}
			}
		}
	}
	return false
}

// rewriteAlwaysInInlineTags rewrites principles:always inside a tags: […] value.
func rewriteAlwaysInInlineTags(bracketed string, hasSession bool) (string, bool, bool) {
	inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(bracketed), "["), "]")
	parts := splitTrimTags(inner)
	seenSession := hasSession
	for _, p := range parts {
		if p == sessionTag {
			seenSession = true
			break
		}
	}
	changed := false
	var out []string
	for _, p := range parts {
		if p == residencyAlwaysTag {
			changed = true
			if seenSession {
				continue
			}
			out = append(out, sessionTag)
			seenSession = true
			continue
		}
		out = append(out, p)
	}
	if !changed {
		return bracketed, false, seenSession
	}
	return "[" + strings.Join(out, ", ") + "]", true, seenSession
}

// replaceFrontmatter swaps the leading YAML frontmatter block for newFM,
// preserving the body prose after the closing ---.
func replaceFrontmatter(body, newFM string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			tail := strings.Join(lines[j+1:], "\n")
			if newFM == "" {
				return "---\n---\n" + tail
			}
			return "---\n" + newFM + "\n---\n" + tail
		}
	}
	return body
}
