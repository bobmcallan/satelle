package docindex

import (
	"path/filepath"
	"strings"
)

// Roots is where each authored kind is read from: the repo's per-kind dirs and,
// in a repo bound to a project tier (sty_8dedc0a4), the tier root whose files
// replace the repo's for every path Owned reports. Owned takes the slash path
// under the data dir ("skills/x.md") — the key the hosted store files it under
// — so a tier file is <Tier>/<kind>/<rel>.
//
// The tier replaces the repo per owned path as a whole set: a repo file an
// owned path names is never read, even when the tier has no copy of it.
type Roots struct {
	Dirs  map[string]string
	Tier  string
	Owned func(rel string) bool
}

// DirRoots is Roots with no project tier: every kind from its repo dir.
func DirRoots(dirs map[string]string) Roots { return Roots{Dirs: dirs} }

func (r Roots) owned(rel string) bool {
	return r.Tier != "" && r.Owned != nil && r.Owned(rel)
}

// TierDir is the project tier's dir for kind, or "" with no tier.
func (r Roots) TierDir(kind string) string {
	if strings.TrimSpace(r.Tier) == "" {
		return ""
	}
	return filepath.Join(r.Tier, kind)
}

// Only narrows r to the named kinds.
func (r Roots) Only(kinds ...string) Roots {
	out := r
	out.Dirs = make(map[string]string, len(kinds))
	for _, k := range kinds {
		if d, ok := r.Dirs[k]; ok {
			out.Dirs[k] = d
		}
	}
	return out
}

// probeUnreadable returns the first of a kind's source dirs (repo, then tier)
// that exists and cannot be read.
func (r Roots) probeUnreadable(kind string) (string, error, bool) {
	for _, d := range []string{strings.TrimSpace(r.Dirs[kind]), r.TierDir(kind)} {
		if d == "" {
			continue
		}
		if state, err := ProbeDir(d); state == DirUnreadable {
			return d, err, true
		}
	}
	return "", nil, false
}

// kindFiles walks a kind's effective files: the repo dir's files no team path
// owns, then the tier's files one does.
func (r Roots) kindFiles(kind string) ([]fileInfo, error) {
	dir := r.Dirs[kind]
	repo, err := walkMarkdown(dir)
	if err != nil || r.Tier == "" {
		return repo, err
	}
	var out []fileInfo
	for _, f := range repo {
		if !r.owned(relKey(kind, dir, f.path)) {
			out = append(out, f)
		}
	}
	tdir := r.TierDir(kind)
	tier, err := walkMarkdown(tdir)
	if err != nil {
		return nil, err
	}
	for _, f := range tier {
		if r.owned(relKey(kind, tdir, f.path)) {
			out = append(out, f)
		}
	}
	return out, nil
}

// relKey is the data-dir-relative key of a file walked under a kind's dir.
func relKey(kind, dir, p string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return kind + "/" + filepath.Base(p)
	}
	return kind + "/" + filepath.ToSlash(rel)
}
