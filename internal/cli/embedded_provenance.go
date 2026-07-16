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
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/substrate"
)

// Stamp helpers live in internal/substrate (sty_ba0eb5c6 extraction). Thin
// aliases keep the rest of the CLI package stable.
const embeddedStampKey = substrate.StampKey

func embeddedSHA(body string) string { return substrate.SHA(body) }

func applyEmbeddedStamp(body, sha string) string { return substrate.ApplyStamp(body, sha) }

func stripEmbeddedStamp(body string) (stripped, stamp string, stamped bool) {
	return substrate.StripStamp(body)
}

// reconcileVerb is the outcome of reconcileEmbeddedFile for one file.
type reconcileVerb string

const (
	reconcileCreated   reconcileVerb = "created"
	reconcileUpdated   reconcileVerb = "updated"
	reconcileUnchanged reconcileVerb = "unchanged"
	reconcileDiverged  reconcileVerb = "diverged"
	// reconcileRestamped: stampless file whose body is byte-identical to the
	// current embedded default — add the stamp only (sty_a9ec33e7). Not an edit.
	reconcileRestamped reconcileVerb = "restamped"
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
		// sty_a9ec33e7: stampless but byte-identical to the current embedded
		// default is PROVABLY safe to re-stamp in place (no substrate meaning
		// change). A different body stays operator-authored and untouched.
		if string(cur) == embeddedBody || stripped == embeddedBody {
			// Pre-mutation backup then stamp.
			bres, berr := backupExistingFile(dataDir, BackupKindPreMutation, relPath, cur, opts)
			if berr != nil {
				return "", BackupResult{}, fmt.Errorf("reconcile: backup %s: %w", dest, berr)
			}
			if werr := writeEmbedded(dest, stampedCurrent); werr != nil {
				return "", BackupResult{}, werr
			}
			return reconcileRestamped, bres, nil
		}
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
	case reconcileRestamped:
		return "  ~ " + disp + " (restamped — identical body, missing embedded_sha)"
	case reconcileDiverged:
		return "  ! " + disp + " (diverged; backed up to " + config.DefaultDataDir + "/backups/diverged/" + relPath + "; needs merge)"
	default: // unchanged
		return initLine(false, disp)
	}
}
