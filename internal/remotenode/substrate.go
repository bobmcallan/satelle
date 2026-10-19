package remotenode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// SubstrateDirs are the directories of a remote definition that become the node's
// substrate, relative to the definition and to the node's data dir alike. The
// definition's remote.toml is not among them: host commands are local satelle's
// business and the node has no use for them.
var SubstrateDirs = []string{"workflows", "skills", "principles"}

// MaxSubstrateBytes bounds the compressed substrate. One relay message is capped
// at 1 MiB and the substrate rides in one, base64-encoded in JSON.
const MaxSubstrateBytes = 512 << 10

// maxUnpackedBytes bounds what a substrate may expand to on the node.
const maxUnpackedBytes = 16 << 20

// PackSubstrate tars and gzips the SubstrateDirs of the definition in dir, in a
// stable order. Only regular files are carried.
func PackSubstrate(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, top := range SubstrateDirs {
		root := filepath.Join(dir, top)
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("remotenode: %s is not a regular file", p)
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			_, err = tw.Write(body)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > MaxSubstrateBytes {
		return nil, fmt.Errorf("remotenode: the remote's substrate is %d bytes compressed; the channel carries at most %d", buf.Len(), MaxSubstrateBytes)
	}
	return buf.Bytes(), nil
}

// UnpackSubstrate replaces the SubstrateDirs under dataDir with the contents of
// packed, so the node's workflows, skills and principles are exactly the
// remote's own and nothing a fresh init scaffolded survives beside them. It
// refuses any entry that is not a regular file under a substrate directory.
func UnpackSubstrate(dataDir string, packed []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return fmt.Errorf("remotenode: substrate: %w", err)
	}
	type file struct {
		rel  string
		body []byte
	}
	var files []file
	total := 0
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("remotenode: substrate: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("remotenode: substrate entry %q is not a regular file", hdr.Name)
		}
		rel := path.Clean(hdr.Name)
		if !allowedSubstratePath(rel) {
			return fmt.Errorf("remotenode: substrate entry %q is outside %s", hdr.Name, strings.Join(SubstrateDirs, ", "))
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxUnpackedBytes+1))
		if err != nil {
			return fmt.Errorf("remotenode: substrate: %w", err)
		}
		if total += len(body); total > maxUnpackedBytes {
			return fmt.Errorf("remotenode: substrate expands past %d bytes", maxUnpackedBytes)
		}
		files = append(files, file{rel, body})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	for _, top := range SubstrateDirs {
		if err := os.RemoveAll(filepath.Join(dataDir, top)); err != nil {
			return err
		}
	}
	for _, f := range files {
		dst := filepath.Join(dataDir, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// allowedSubstratePath: a clean, relative, slash path under a SubstrateDirs entry.
func allowedSubstratePath(rel string) bool {
	if rel == "." || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	top, rest, ok := strings.Cut(rel, "/")
	if !ok || rest == "" {
		return false
	}
	for _, d := range SubstrateDirs {
		if top == d {
			return true
		}
	}
	return false
}
