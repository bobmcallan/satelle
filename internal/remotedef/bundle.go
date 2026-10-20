package remotedef

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/filebundle"
)

// definitionRoots are the paths inside a remote directory that make up the
// definition: remote.toml and the three directories validation reads. Anything
// else in the directory is not part of what is published.
var definitionRoots = []string{DefinitionFile, workflowsDir, skillsDir, principlesDir}

// isDefinitionPath reports whether a slash path is part of the definition.
func isDefinitionPath(rel string) bool {
	for _, root := range definitionRoots {
		if rel == root || strings.HasPrefix(rel, root+"/") {
			return true
		}
	}
	return false
}

// Bundle encodes the definition in dir as one deterministic blob, so a remote is
// one published artifact with one version number. Files are taken in
// walkDefinition order (definitionRoots order, lexical within each root) and
// the encoding (internal/filebundle) is stable, so the same definition always
// yields the same bytes. A .local file never enters a bundle, and content that
// is not UTF-8 is refused: a definition is text.
func Bundle(dir string) ([]byte, error) {
	var entries []filebundle.Entry
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
		entries = append(entries, filebundle.Entry{Path: rel, Content: string(body)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return filebundle.Encode(entries)
}

// Unbundle writes the files of a bundle into dir, byte for byte. Every path is
// checked before anything is written: an absolute path, one that escapes dir, a
// .local path, one outside the definition roots, or an unknown format is
// refused. A non-empty dir is refused unless overwrite is set; overwrite
// replaces the definition files of dir with the bundle's, removing definition
// files the bundle does not carry (a .local file is never removed).
func Unbundle(dir string, blob []byte, overwrite bool) error {
	entries, err := filebundle.Decode(blob, definitionRoots)
	if err != nil {
		var malformed *filebundle.MalformedError
		var format *filebundle.FormatError
		switch {
		case errors.As(err, &malformed):
			return fmt.Errorf("not a remote bundle: %w", malformed.Err)
		case errors.As(err, &format):
			return fmt.Errorf("unknown remote bundle format %d (this satelle reads %d)", format.Got, format.Want)
		}
		return err
	}
	return filebundle.Write(dir, entries, overwrite, isDefinitionPath)
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
