package config

// The seed-provenance stamp and the one classifier for "is this file an unedited
// embedded default". It lives in config because the sync bundle (assemble) needs
// it and substrate imports config: one rule, read by list, prune, init, push and
// pull (sty_097833e8).

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// StampKey is the frontmatter key carrying the seed provenance hash.
const StampKey = "embedded_sha"

// IsUneditedSeed reports whether on-disk body is an unedited copy of embedded
// (stamped or stampless identical). Shared with prune.
func IsUneditedSeed(diskBody, embeddedBody string) bool {
	stripped, stamp, stamped := StripStamp(diskBody)
	if stamped {
		// Unedited seed of current OR older default (stamp matches stripped body).
		return SHA(stripped) == stamp
	}
	// Stampless but byte-identical to embedded.
	return diskBody == embeddedBody || stripped == embeddedBody
}

var (
	embeddedByRelOnce sync.Once
	embeddedByRel     map[string]string
)

// embeddedBodyByRel maps an embedded default's kind-relative path (the form a
// sync bundle names it by) to its body.
func embeddedBodyByRel() map[string]string {
	embeddedByRelOnce.Do(func() {
		embeddedByRel = map[string]string{}
		for _, d := range EmbeddedDefaults() {
			embeddedByRel[d.RelPath()] = d.Body
		}
	})
	return embeddedByRel
}

// UneditedDefault reports whether the file at server-relative path rel with the
// given bytes is an unedited embedded default: a copy whose stamp matches its
// stripped body (of the current or an older default), or a stampless copy
// byte-identical to the current embedded default. Such a file is not substrate
// the operator wrote — the installed binary supplies it — so it is never
// versioned, pushed, or written over the tree by a pull.
func UneditedDefault(rel string, body []byte) bool {
	text := string(body)
	if _, _, stamped := StripStamp(text); stamped {
		return IsUneditedSeed(text, "")
	}
	emb, ok := embeddedBodyByRel()[rel]
	return ok && IsUneditedSeed(text, emb)
}

// UneditedSeedPaths lists the embedded defaults — tasks included — whose copy
// under dataDir is an unedited seed, as dataDir-relative slash paths, sorted.
// The one enumeration behind `satelle substrate prune` and `satelle migrate`.
func UneditedSeedPaths(dataDir string) []string {
	var out []string
	for _, d := range EmbeddedDefaults() {
		rel := d.RelPath()
		b, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		if IsUneditedSeed(string(b), d.Body) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// SHA is the hex sha256 of an embedded body — the same hashing docindex uses.
func SHA(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// ApplyStamp inserts the seed-provenance stamp as the LAST frontmatter line of
// body, in whichever form the file is authored in (sty_81bb0dde):
//
//	---                          [meta]
//	name: x                      name = "x"
//	embedded_sha: <sha>          embedded_sha = "<sha>"
//	---
//
// FORMAT IS SNIFFED, the same way docindex.Frontmatter sniffs it: a body opening
// with `---` is markdown, anything else is TOML. A file with neither is returned
// unchanged — an unstampable body is operator-authored by definition, and the
// reconcile table already treats "no stamp" as untouchable.
//
// ApplyStamp and StripStamp must remain EXACT byte inverses in both forms, or
// re-stamping an unedited seed registers as an operator edit and the file is
// backed up and surfaced on every init.
func ApplyStamp(body, sha string) string {
	lines := strings.Split(body, "\n")
	if at, ok := stampInsertAt(lines); ok {
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:at]...)
		out = append(out, stampLine(lines, sha))
		out = append(out, lines[at:]...)
		return strings.Join(out, "\n")
	}
	return body
}

// StripStamp removes the single stamp line from body's frontmatter. Exact
// inverse of ApplyStamp, in both forms.
func StripStamp(body string) (stripped, stamp string, stamped bool) {
	lines := strings.Split(body, "\n")
	end, ok := frontmatterEnd(lines)
	if !ok {
		return body, "", false
	}
	for j := 1; j < end; j++ {
		t := strings.TrimSpace(lines[j])
		rest, hit := strings.CutPrefix(t, StampKey+":")
		if !hit {
			if rest, hit = strings.CutPrefix(t, StampKey+" ="); !hit {
				if rest, hit = strings.CutPrefix(t, StampKey+"="); !hit {
					continue
				}
			}
		}
		stamp = strings.Trim(strings.TrimSpace(rest), `"`)
		out := make([]string, 0, len(lines)-1)
		out = append(out, lines[:j]...)
		out = append(out, lines[j+1:]...)
		return strings.Join(out, "\n"), stamp, true
	}
	return body, "", false
}

// isTOMLFrontmatter reports whether the body uses the `[meta]` form.
func isTOMLFrontmatter(lines []string) bool {
	return len(lines) > 0 && strings.TrimSpace(lines[0]) == "[meta]"
}

// frontmatterEnd returns the index one past the last frontmatter line — the
// closing `---` for markdown, or the first line that opens another table for
// TOML. ok is false when the body carries no frontmatter this package can read.
func frontmatterEnd(lines []string) (int, bool) {
	if len(lines) == 0 {
		return 0, false
	}
	if strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				return j, true
			}
		}
		return 0, false
	}
	if !isTOMLFrontmatter(lines) {
		return 0, false
	}
	for j := 1; j < len(lines); j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
			return j, true
		}
	}
	return len(lines), true
}

// stampInsertAt returns the index the stamp line is inserted at: immediately
// before the closing `---`, or — for TOML, where there is no closing marker —
// after the meta table's last key, skipping the blank lines and comments that
// separate it from the first record.
func stampInsertAt(lines []string) (int, bool) {
	end, ok := frontmatterEnd(lines)
	if !ok {
		return 0, false
	}
	if strings.TrimSpace(lines[0]) == "---" {
		return end, true
	}
	for end > 1 {
		t := strings.TrimSpace(lines[end-1])
		if t == "" || strings.HasPrefix(t, "#") {
			end--
			continue
		}
		break
	}
	return end, true
}

// stampLine renders the stamp in the body's own key syntax.
func stampLine(lines []string, sha string) string {
	if isTOMLFrontmatter(lines) {
		return StampKey + ` = "` + sha + `"`
	}
	return StampKey + ": " + sha
}
