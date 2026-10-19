package snapsync

import (
	"sort"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// DefaultSet is the one owner of "is this base or snapshot entry a provable
// unedited embedded default", judged by sha. The installed binary supplies such a
// file, so a local tree never holds it (config.AreaFiles drops it) and a pull never
// writes it: an entry the hosted side holds and the local view lacks is therefore
// not a change on either side. Driver.SkipBody is the byte-level authority for the
// same question; this is its sha projection (config.AreaDefaultShas), which the
// offline and fetch-free comparisons need, plus the shas a pull recorded in
// Base.Defaults after SkipBody accepted bytes the projection could not predict.
//
// The zero value holds nothing, which is today's strict comparison.
type DefaultSet struct {
	shas map[string]map[string]bool
	base map[string]string
}

// NewDefaultSet joins the sha projection of the area's embedded defaults with the
// defaults the recorded base already proved. base may be nil.
func NewDefaultSet(shas map[string]map[string]bool, base *hosted.AreaBase) DefaultSet {
	s := DefaultSet{shas: shas}
	if base != nil {
		s.base = base.Defaults
	}
	return s
}

// Holds reports whether sha is, for path, an unedited embedded default.
func (s DefaultSet) Holds(path, sha string) bool {
	if sha == "" {
		return false
	}
	return s.shas[path][sha] || s.base[path] == sha
}

// changedPaths lists, sorted, the paths whose sha differs between a and b. A path
// only one side names is ignored when that side's sha is a held default: the other
// side supplies it from the binary instead of from a file.
func (s DefaultSet) changedPaths(a, b map[string]string) []string {
	var out []string
	for p, sha := range a {
		other, ok := b[p]
		switch {
		case ok && other == sha:
		case !ok && s.Holds(p, sha):
		default:
			out = append(out, p)
		}
	}
	for p, sha := range b {
		if _, ok := a[p]; !ok && !s.Holds(p, sha) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// sameFilesExcept reports whether a and b name the same files with the same shas,
// not counting an entry only one side names whose sha is a held default.
func (s DefaultSet) sameFilesExcept(a, b map[string]string) bool {
	return len(s.changedPaths(a, b)) == 0
}
