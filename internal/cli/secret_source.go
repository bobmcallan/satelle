package cli

// Where a declared secret's value comes from on the developer's machine
// (sty_0b1c72a9): the local .env file, or a command that mints it. This is the one
// place satelle runs a `from` command; nothing else spawns one.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/procgroup"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// secretSourceWait bounds how long a killed `from` command's output pipes may stay
// open before the read gives up on them.
const secretSourceWait = 5 * time.Second

// runSecretSource runs a secret's `from` argv directly (no shell) in dir and
// returns its stdout with the trailing newline trimmed. The command runs in its own
// process group, so a cancelled context kills it and anything it started. The
// value is never logged and the command's stderr is discarded, since either may
// hold it; an error names the command, never its output.
func runSecretSource(ctx context.Context, argv []string, dir string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command to run")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	procgroup.Set(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = secretSourceWait
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		return "", fmt.Errorf("%s failed: %w", argv[0], err)
	}
	v := strings.TrimRight(stdout.String(), "\r\n")
	if v == "" {
		return "", fmt.Errorf("%s printed no value", argv[0])
	}
	return v, nil
}

// secretSources reads the values of a definition's secrets: from the .env file
// (read once, on first need) or by running the declaration's command.
type secretSources struct {
	dataDir, repoRoot string
	dotenv            map[string]string
	loaded            bool
}

// value is the current value of the declared secret, or why there is none.
func (s *secretSources) value(ctx context.Context, d remotedef.SecretDecl) (string, error) {
	if d.IsEnv() {
		if !s.loaded {
			vals, err := config.ReadDotEnv(s.dataDir)
			if err != nil {
				return "", err
			}
			s.dotenv, s.loaded = vals, true
		}
		v, ok := s.dotenv[d.Key]
		if !ok || v == "" {
			return "", fmt.Errorf("%s has no value in %s/%s", d.Key, config.DefaultDataDir, config.DotEnvFile)
		}
		return v, nil
	}
	return runSecretSource(ctx, d.Argv(), s.repoRoot)
}
