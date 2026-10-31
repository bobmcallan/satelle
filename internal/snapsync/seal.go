package snapsync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// sealBase is the seal the driver puts on a base it records: a sha256 over the
// area, the version and every recorded entry, never over the seal itself. The
// driver legitimately records bases whose files differ from the snapshot of their
// version (a first claim carrying hosted files forward, an incomplete path keeping
// its old sha), so it is the seal, not a file comparison, that says a base is one
// a push or pull recorded.
func sealBase(area string, b hosted.AreaBase) string {
	h := sha256.New()
	fmt.Fprintf(h, "area %q\nversion %d\n", area, b.Version)
	for _, g := range []struct {
		kind  string
		files map[string]string
	}{{"file", b.Files}, {"unmerged", b.Unmerged}, {"default", b.Defaults}} {
		paths := make([]string, 0, len(g.files))
		for p := range g.files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fmt.Fprintf(h, "%s %q %s\n", g.kind, p, g.files[p])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sealed reports whether the base carries the seal of exactly its own content.
func sealed(area string, b hosted.AreaBase) bool {
	return b.Seal != "" && b.Seal == sealBase(area, b)
}

// saveBase records a base the driver wrote, sealed. Every base the driver saves
// goes through it.
func (d *Driver) saveBase(area string, b hosted.AreaBase) error {
	b.Seal = sealBase(area, b)
	return hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, area, b)
}

// unrecordedBase is the refusal for a base no push or pull recorded (unsealed, or
// sealed over other content) that names the hosted effective snapshot while its
// files differ from that snapshot's: a marker moved without a sync. Which side is
// newer cannot be told, so the area is diverged, and the reconcile that shows both
// sides is the way on — never a pull that would overwrite the local files. A
// path the base already holds unmerged is not counted. nil means no refusal.
func unrecordedBase(area string, base *hosted.AreaBase, isSealed bool, effVersion int, eff Record, set DefaultSet, divergedCmd string) *ErrBehind {
	if base == nil || isSealed || effVersion == 0 || base.Version != effVersion {
		return nil
	}
	var differ []string
	for _, p := range set.changedPaths(base.Files, eff.Files) {
		if _, un := base.Unmerged[p]; !un {
			differ = append(differ, p)
		}
	}
	if len(differ) == 0 {
		return nil
	}
	return &ErrBehind{Area: area, Base: base.Version, Effective: effVersion, Pending: differ, Diverged: true, Unrecorded: true, PullCmd: divergedCmd}
}
