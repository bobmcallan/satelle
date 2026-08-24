package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

const logsPointerRel = ".satelle/logs"

// EnsureLogsPointer plants .satelle/logs as a symlink to the home-keyed runtime
// logs directory. It refuses to clobber a real directory.
func EnsureLogsPointer(repoRoot, runtimeLogs string) error {
	if err := os.MkdirAll(runtimeLogs, 0o755); err != nil {
		return err
	}
	link := filepath.Join(repoRoot, filepath.FromSlash(logsPointerRel))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	st, err := os.Lstat(link)
	if err == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a symlink — migrate relocates a real logs dir first", logsPointerRel)
		}
		got, _ := os.Readlink(link)
		want, _ := filepath.Abs(runtimeLogs)
		if got == runtimeLogs || got == want {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	target, err := filepath.Abs(runtimeLogs)
	if err != nil {
		target = runtimeLogs
	}
	return os.Symlink(target, link)
}

func gitignoreTextIgnoresSatelleDir(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch line {
		case ".satelle", ".satelle/", "/.satelle", "/.satelle/":
			return true
		}
	}
	return false
}

func plantLogsPointer(out io.Writer, repoRoot string) {
	cfgPath := filepath.Join(repoRoot, config.DefaultDataDir, config.ConfigName)
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		cfg, _, err = config.Load("")
	}
	if err != nil {
		fmt.Fprintf(out, "  ! %s pointer: %v\n", logsPointerRel, err)
		return
	}
	rt := cfg.ResolveLogsDir(repoRoot)
	link := filepath.Join(repoRoot, filepath.FromSlash(logsPointerRel))
	_, existed := os.Lstat(link)
	if err := EnsureLogsPointer(repoRoot, rt); err != nil {
		fmt.Fprintf(out, "  ! %s pointer: %v\n", logsPointerRel, err)
		return
	}
	fmt.Fprintln(out, initLine(existed != nil, logsPointerRel+" (pointer to runtime logs)"))
}
