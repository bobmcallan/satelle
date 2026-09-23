// Package fsmove relocates a file, symlink or directory tree without ever
// deleting content it has not first copied. It is the one move primitive shared
// by the leftovers sweep and `satelle story tidy` (sty_d74e9b1b).
package fsmove

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Move renames src to dst, creating dst's parent directories. When a plain
// rename fails (typically across devices — the scratch area is usually on a
// different filesystem from the repo) it copies the whole tree and only then
// removes src; a failed copy leaves src untouched and removes the partial dst.
// A symlink is moved as a link and never followed. Move refuses to overwrite an
// existing dst.
func Move(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("fsmove: destination exists: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(p, target, info.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
