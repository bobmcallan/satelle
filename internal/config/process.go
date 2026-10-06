package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// InvokingRoot is the tree a location config was loaded for. A set location
// path names it (the directory that holds that file's data dir). Otherwise it
// is the working directory. LoadProcess uses this and does not take a root
// from its caller, so a hook cannot pass the main tree and receive the
// location config back.
func InvokingRoot(locationPath string) string {
	if strings.TrimSpace(locationPath) != "" {
		return RepoRootFromConfigPath(locationPath)
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// LoadProcess resolves the process-of-record config for a location load.
//
// location and locationPath are the result of config.Load for the invoking
// tree (the write-side config). When the canonical main tree differs from
// that invoking root, the process of record is main's
// <DefaultDataDir>/<ConfigName>, including the local overlay beside that
// file. When they are the same tree, the location config is the process
// config and nothing is read again.
//
// An explicit SATELLE_CONFIG wins over the canonical load: the location
// config is returned and the process root is that file's root, not main.
//
// A missing main file is os.ErrNotExist from the explicit read, not
// ErrNotFound. That stores a zero Config, sets the process root to main, and
// returns the missing path with a nil error. A parse error, an overlay parse
// error, or any other error is returned.
//
// processPath is the file whose bytes are the process of record — the path
// Load opened, or the path that was missing. It is empty when the invoking
// tree is the main tree and that tree has no config file.
func LoadProcess(location Config, locationPath string) (process Config, processRoot, processPath string, err error) {
	if strings.TrimSpace(os.Getenv("SATELLE_CONFIG")) != "" {
		return location, InvokingRoot(locationPath), locationPath, nil
	}
	invoking := InvokingRoot(locationPath)
	main := CanonicalRepoRoot(invoking)
	if sameResolvedPath(main, invoking) {
		return location, invoking, locationPath, nil
	}
	processPath = filepath.Join(main, DefaultDataDir, ConfigName)
	cfg, path, lerr := Load(processPath)
	if lerr != nil {
		if errors.Is(lerr, os.ErrNotExist) {
			if path == "" {
				path = processPath
			}
			return Config{}, main, path, nil
		}
		return Config{}, "", "", lerr
	}
	return cfg, main, path, nil
}

// ResolveProcessDataDir is ResolveDataDir for the process-of-record config,
// anchored at the process root.
func ResolveProcessDataDir(process Config, processRoot string) string {
	return process.ResolveDataDir(processRoot)
}

// ResolveProcessAuthoredDirs is ResolveAuthoredDirs for the process-of-record
// config, anchored at the process root. A relative substrate root is joined
// to that root; an absolute one passes through.
func ResolveProcessAuthoredDirs(process Config, processRoot string) map[string]string {
	return process.ResolveAuthoredDirs(processRoot)
}

// ResolveProcessConstitution is ResolveConstitution for the process-of-record
// config, anchored at the process root.
func ResolveProcessConstitution(process Config, processRoot string) string {
	return process.ResolveConstitution(processRoot)
}

// LoadInvokingProcess loads the location config from the working directory
// and the process of record for it. A missing location file is a zero
// location config, not an error. LoadProcess derives the invoking root
// itself; the caller does not pass one. err is a parse error or any other
// load error. A missing process file is a zero Config, the main-tree root,
// and the missing path, with a nil error.
func LoadInvokingProcess() (process Config, invokingRoot, processRoot, processPath string, err error) {
	loc, locPath, lerr := Load("")
	if lerr != nil && !errors.Is(lerr, ErrNotFound) {
		return Config{}, "", "", "", lerr
	}
	process, processRoot, processPath, err = LoadProcess(loc, locPath)
	if err != nil {
		return Config{}, "", "", "", err
	}
	return process, InvokingRoot(locPath), processRoot, processPath, nil
}

// sameResolvedPath reports whether a and b name the same directory after
// absolutising and resolving symlinks. A missing path still compares equal
// to itself on the cleaned absolute form.
func sameResolvedPath(a, b string) bool {
	aa, bb := absClean(a), absClean(b)
	if aa == bb {
		return true
	}
	if ra, err := filepath.EvalSymlinks(aa); err == nil {
		aa = ra
	}
	if rb, err := filepath.EvalSymlinks(bb); err == nil {
		bb = rb
	}
	return aa == bb
}

func absClean(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}
