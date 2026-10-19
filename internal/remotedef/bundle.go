package remotedef

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/bobmcallan/satelle/internal/config"
)

// bundleFormat is the version of the bundle encoding. Unbundle refuses any other.
const bundleFormat = 1

// definitionRoots are the paths inside a remote directory that make up the
// definition: remote.toml and the three directories validation reads. Anything
// else in the directory is not part of what is published.
var definitionRoots = []string{DefinitionFile, workflowsDir, skillsDir, principlesDir}

type bundle struct {
	Format int          `json:"format"`
	Files  []bundleFile `json:"files"`
}

type bundleFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Bundle encodes the definition in dir as one deterministic blob, so a remote is
// one published artifact with one version number. Files are taken in sorted path
// order and the encoding is stable, so the same definition always yields the
// same bytes. A .local file never enters a bundle, and content that is not
// UTF-8 is refused: a definition is text.
func Bundle(dir string) ([]byte, error) {
	b := bundle{Format: bundleFormat, Files: []bundleFile{}}
	err := walkDefinition(dir, func(rel string, d fs.DirEntry) error {
		if config.LocalOnlyPath(rel) {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", rel)
		}
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if !utf8.Valid(body) {
			return fmt.Errorf("%s: not UTF-8 text", rel)
		}
		b.Files = append(b.Files, bundleFile{Path: rel, Content: string(body)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

// Unbundle writes the files of a bundle into dir, byte for byte. Every path is
// checked before anything is written: an absolute path, one that escapes dir, a
// .local path, one outside the definition roots, or an unknown format is
// refused. A non-empty dir is refused unless overwrite is set; overwrite
// replaces the definition files of dir with the bundle's, removing definition
// files the bundle does not carry (a .local file is never removed).
func Unbundle(dir string, blob []byte, overwrite bool) error {
	var b bundle
	if err := json.Unmarshal(blob, &b); err != nil {
		return fmt.Errorf("not a remote bundle: %w", err)
	}
	if b.Format != bundleFormat {
		return fmt.Errorf("unknown remote bundle format %d (this satelle reads %d)", b.Format, bundleFormat)
	}
	keep := map[string]bool{}
	for _, f := range b.Files {
		if err := checkBundlePath(f.Path); err != nil {
			return err
		}
		keep[f.Path] = true
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 && !overwrite {
		return fmt.Errorf("%s is not empty — pass --force to overwrite it", dir)
	}
	if overwrite {
		err := walkDefinition(dir, func(rel string, _ fs.DirEntry) error {
			if keep[rel] || config.LocalOnlyPath(rel) {
				return nil
			}
			return os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
		})
		if err != nil {
			return err
		}
	}
	for _, f := range b.Files {
		dest := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func checkBundlePath(p string) error {
	if p == "" || path.IsAbs(p) || strings.Contains(p, "\\") || path.Clean(p) != p {
		return fmt.Errorf("bundle path %q is not a plain relative path", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("bundle path %q escapes the remote directory", p)
		}
	}
	if config.LocalOnlyPath(p) {
		return fmt.Errorf("bundle path %q is a .local file, which never travels", p)
	}
	for _, root := range definitionRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return nil
		}
	}
	return fmt.Errorf("bundle path %q is outside the remote definition", p)
}

// walkDefinition calls fn for every file under the definition roots of dir,
// each root in turn and its files in sorted order, with the file's slash path
// relative to dir. A root that is absent is skipped.
func walkDefinition(dir string, fn func(rel string, d fs.DirEntry) error) error {
	for _, root := range definitionRoots {
		err := filepath.WalkDir(filepath.Join(dir, filepath.FromSlash(root)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			return fn(filepath.ToSlash(rel), d)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
