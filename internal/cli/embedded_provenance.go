// embedded_provenance.go — file-local provenance for embedded-owned substrate
// (sty_304ee454). init/rebase STAMP every embedded default they materialise with
// a frontmatter `embedded_sha:` line = sha256 of the embedded body as shipped.
// On re-init the stamp lets us CONVERGE an UNEDITED stale copy to the current
// binary while never clobbering an operator edit:
//
//   - no stamp                          -> operator-authored -> UNTOUCHED
//   - stamp, strip-hash == stamp:
//     stamp == sha(current embedded)  -> unchanged (no-op)
//     stamp != sha(current embedded)  -> binary advanced -> OVERWRITE + re-stamp
//   - stamp, strip-hash != stamp        -> operator EDITED -> back up, surface, keep
//
// The stamp is the ONLY line added on write, and applyEmbeddedStamp /
// stripEmbeddedStamp are exact byte inverses, so re-stamping an unedited file
// never registers as an edit (AC6).
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// embeddedStampKey is the frontmatter key carrying the seed provenance hash.
const embeddedStampKey = "embedded_sha"

// embeddedSHA is the hex sha256 of an embedded body — the same hashing docindex
// uses for its content hash. Computed over the body EXACTLY as shipped (an
// embedded default never itself carries an embedded_sha line).
func embeddedSHA(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// applyEmbeddedStamp inserts `embedded_sha: <sha>` as the LAST frontmatter line
// of body (immediately before the closing `---`). A body with no terminated
// frontmatter is returned unchanged (defensive; every embedded default has
// frontmatter — a test guards that).
func applyEmbeddedStamp(body, sha string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			out := make([]string, 0, len(lines)+1)
			out = append(out, lines[:j]...)
			out = append(out, embeddedStampKey+": "+sha)
			out = append(out, lines[j:]...)
			return strings.Join(out, "\n")
		}
	}
	return body // unterminated frontmatter — leave untouched
}

// stripEmbeddedStamp removes the single embedded_sha line from body's frontmatter,
// returning the stripped body, the stamp value, and whether a stamp was present.
// Exact inverse of applyEmbeddedStamp — load-bearing for AC6: uses line split/join
// (not a lossy frontmatter rest-join) so
// stripEmbeddedStamp(applyEmbeddedStamp(b, s)) reproduces b byte-for-byte.
func stripEmbeddedStamp(body string) (stripped, stamp string, stamped bool) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body, "", false
	}
	pre := embeddedStampKey + ":"
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return body, "", false // end of frontmatter, no stamp
		}
		if t := strings.TrimSpace(lines[j]); t == pre || strings.HasPrefix(t, pre+" ") {
			stamp = strings.TrimSpace(strings.TrimPrefix(t, pre))
			out := make([]string, 0, len(lines)-1)
			out = append(out, lines[:j]...)
			out = append(out, lines[j+1:]...)
			return strings.Join(out, "\n"), stamp, true
		}
	}
	return body, "", false
}

// reconcileVerb is the outcome of reconcileEmbeddedFile for one file.
type reconcileVerb string

const (
	reconcileCreated   reconcileVerb = "created"
	reconcileUpdated   reconcileVerb = "updated"
	reconcileUnchanged reconcileVerb = "unchanged"
	reconcileDiverged  reconcileVerb = "diverged"
)

// reconcileEmbeddedFile materialises/converges one embedded-owned file at
// dataDir/relPath from embeddedBody, per the decision table at the top of this
// file. On divergence or converge-overwrite it backs the on-disk file up first
// via the shared helper (sty_873a5380) under dataDir/backups/<kind>/<relPath>
// and leaves the original in place on diverge. Files are written 0o644
// (editable substrate, not a generated 0o444 view). relPath is the kind-relative
// slash path (e.g. "principles/x.md"). Returns the verb and the backup path
// (diverge / update only).
//
// backupOpts is optional (zero value → local floor + advisory when no hosted).
// The BackupResult carries LocalPath (when a pre-image was written) and Notice
// (hosted destination / advisory) for the caller to surface.
func reconcileEmbeddedFile(dataDir, relPath, embeddedBody string, backupOpts ...BackupOpts) (reconcileVerb, BackupResult, error) {
	var opts BackupOpts
	if len(backupOpts) > 0 {
		opts = backupOpts[0]
	}
	dest := filepath.Join(dataDir, filepath.FromSlash(relPath))
	stampedCurrent := applyEmbeddedStamp(embeddedBody, embeddedSHA(embeddedBody))

	cur, err := os.ReadFile(dest)
	if os.IsNotExist(err) {
		if werr := writeEmbedded(dest, stampedCurrent); werr != nil {
			return "", BackupResult{}, werr
		}
		return reconcileCreated, BackupResult{}, nil
	}
	if err != nil {
		return "", BackupResult{}, fmt.Errorf("reconcile: read %s: %w", dest, err)
	}

	stripped, stamp, stamped := stripEmbeddedStamp(string(cur))
	if !stamped {
		return reconcileUnchanged, BackupResult{}, nil // operator-authored (no stamp) — untouched
	}
	if embeddedSHA(stripped) != stamp {
		// Operator edited the body since it was stamped — never clobber.
		bres, berr := backupExistingFile(dataDir, BackupKindDiverged, relPath, cur, opts)
		if berr != nil {
			return "", BackupResult{}, fmt.Errorf("reconcile: backup %s: %w", dest, berr)
		}
		return reconcileDiverged, bres, nil
	}
	// Unedited seed. Converge to the current embedded body only if it moved on.
	if stamp == embeddedSHA(embeddedBody) {
		return reconcileUnchanged, BackupResult{}, nil
	}
	// Pre-mutation backup before overwrite (sty_873a5380).
	bres, berr := backupExistingFile(dataDir, BackupKindPreMutation, relPath, cur, opts)
	if berr != nil {
		return "", BackupResult{}, fmt.Errorf("reconcile: backup %s: %w", dest, berr)
	}
	if werr := writeEmbedded(dest, stampedCurrent); werr != nil {
		return "", BackupResult{}, werr
	}
	return reconcileUpdated, bres, nil
}

// writeEmbedded writes editable substrate (0o644), creating parent dirs.
func writeEmbedded(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("reconcile: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("reconcile: write %s: %w", path, err)
	}
	return nil
}

// reconcileReportLine renders the init report line for a reconcile outcome, in
// the existing initLine register. relPath is the kind-relative slash path.
func reconcileReportLine(verb reconcileVerb, relPath string) string {
	disp := config.DefaultDataDir + "/" + relPath
	switch verb {
	case reconcileCreated:
		return initLine(true, disp)
	case reconcileUpdated:
		return "  ~ " + disp + " (updated from embedded)"
	case reconcileDiverged:
		return "  ! " + disp + " (diverged; backed up to " + config.DefaultDataDir + "/backups/diverged/" + relPath + "; needs merge)"
	default: // unchanged
		return initLine(false, disp)
	}
}
