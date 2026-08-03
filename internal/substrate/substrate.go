// Package substrate classifies effective process docs (workflows, skills,
// principles) as default|edited|authored for the CLI list surface and the web
// effective-process view (sty_29e5a9a5 / sty_ba0eb5c6).
//
// Pure over a docindex.Store + embedded defaults — no I/O beyond reading disk
// bodies when a path is real. documents and tasks are excluded (free-form and
// workitem plane).
package substrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// Row is one effective substrate item with provenance.
type Row struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Provenance string `json:"provenance"` // default | edited | authored
	Embedded   bool   `json:"embedded"`
}

// StampKey is the frontmatter key carrying the seed provenance hash.
const StampKey = "embedded_sha"

// List returns effective process rows (workflows/skills/principles), classified
// and sorted by kind then name. Skips documents and tasks.
func List(ctx context.Context, idx *docindex.Store) ([]Row, error) {
	docs, err := idx.List(ctx, "")
	if err != nil {
		return nil, err
	}
	emb := EmbeddedBodies()
	var rows []Row
	for _, d := range docs {
		if d.Kind == "documents" || d.Kind == "tasks" {
			continue
		}
		rows = append(rows, Row{
			Kind: d.Kind, Name: d.Name, Source: d.Path,
			Provenance: Classify(d, emb), Embedded: d.Embedded,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// EmbeddedBodies maps kind\x00name → body for non-task embedded defaults.
func EmbeddedBodies() map[string]string {
	emb := map[string]string{}
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			continue
		}
		emb[d.Kind+"\x00"+d.Name] = d.Body
	}
	return emb
}

// Classify returns default|edited|authored for one List row.
func Classify(d docindex.Doc, emb map[string]string) string {
	key := d.Kind + "\x00" + d.Name
	body, hasEmb := emb[key]
	if d.Embedded || !fileExists(d.Path) {
		if hasEmb {
			return "default"
		}
		return "authored"
	}
	if !hasEmb {
		return "authored"
	}
	if IsUneditedSeed(string(mustRead(d.Path)), body) {
		return "default" // on disk but identical → still "default" provenance
	}
	return "edited"
}

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

func fileExists(p string) bool {
	if p == "" || strings.HasPrefix(p, "embedded:") {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}
