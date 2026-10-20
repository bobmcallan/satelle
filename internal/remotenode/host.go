package remotenode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satelle/internal/procgroup"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// KeyLinePrefix starts the stdout line on which the node prints its public key.
// The controller reads the key from the launch it started, never from the
// channel, and pins it.
const KeyLinePrefix = "satelle-node-key "

// NodeRoot is the default working root on the node, as the node's shell spells it.
const NodeRoot = "~/satelle-node"

// Commands runs the identity token command of one remote definition on the node.
// It is the user's own tooling; Commands fills its placeholders and runs it in a
// process group.
type Commands struct {
	Def remotedef.Definition
}

// Token runs the definition's identity token command with {aud} filled and returns
// the token it prints. It is how a node on a platform with workload identity
// proves itself for one run: its output is the token and nothing else, so it is
// never logged.
func (c *Commands) Token(ctx context.Context, aud string) (string, error) {
	argv := c.Def.TokenArgv(aud)
	if len(argv) == 0 {
		return "", errors.New("the definition declares no identity.oidc token command")
	}
	cmd := c.command(ctx, argv)
	var stdout bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("identity token command: %w", err)
	}
	tok := strings.TrimSpace(stdout.String())
	if tok == "" {
		return "", errors.New("the identity token command printed no token")
	}
	return tok, nil
}

func (c *Commands) command(ctx context.Context, argv []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	procgroup.Set(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	return cmd
}
