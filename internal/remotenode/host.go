package remotenode

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/procgroup"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// KeyLinePrefix starts the stdout line on which the node prints its public key.
// Local satelle reads the key from the host command it launched, never from the
// channel, and pins it.
const KeyLinePrefix = "satelle-node-key "

// killWait is how long Kill waits for a killed host command to be reaped.
const killWait = 5 * time.Second

// Commands runs the host commands of one remote definition. They are the user's
// own tooling; Commands fills their placeholders, runs them in a process group
// and knows nothing of the host behind them.
type Commands struct {
	Def remotedef.Definition
	// Env is KEY=VALUE pairs added to the environment host commands run under (the
	// definition's host.env, already expanded).
	Env []string
	// Log receives the host commands' output; nil discards it.
	Log io.Writer
}

func (c *Commands) log() io.Writer {
	if c.Log == nil {
		return io.Discard
	}
	return c.Log
}

func (c *Commands) label() string { return c.Def.Identity.Label }

// Fill replaces each placeholder in every element of argv. pairs is
// placeholder, value, placeholder, value, …
func Fill(argv []string, pairs ...string) []string {
	r := strings.NewReplacer(pairs...)
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = r.Replace(a)
	}
	return out
}

// Start runs host.start to completion.
func (c *Commands) Start(ctx context.Context) error {
	return c.run(ctx, "host.start", Fill(c.Def.Host.Start, "{label}", c.label()))
}

// Network runs host.network to completion with {allow} filled by the allowlist,
// comma-joined.
func (c *Commands) Network(ctx context.Context, allow []string) error {
	return c.run(ctx, "host.network", Fill(c.Def.Host.Network, "{label}", c.label(), remotedef.PlaceholderAllow, strings.Join(allow, ",")))
}

// Stop runs host.stop to completion.
func (c *Commands) Stop(ctx context.Context) error {
	return c.run(ctx, "host.stop", Fill(c.Def.Host.Stop, "{label}", c.label()))
}

// run executes argv to completion, in its own process group, killed with the
// whole group when ctx ends.
func (c *Commands) run(ctx context.Context, what string, argv []string) error {
	cmd := c.command(ctx, argv)
	cmd.Stdout, cmd.Stderr = c.log(), c.log()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func (c *Commands) command(ctx context.Context, argv []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), c.Env...)
	procgroup.Set(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	return cmd
}

// Held is a host.run process kept open for the length of a run. A host that
// freezes when its last exec session ends needs someone to hold one; that is
// local satelle, for as long as the run lasts.
type Held struct {
	cmd  *exec.Cmd
	key  chan keyResult
	done chan struct{}
}

type keyResult struct {
	key []byte
	err error
}

// Hold starts host.run with {command} filled and keeps it running. It does not
// wait for it.
func (c *Commands) Hold(ctx context.Context, command string) (*Held, error) {
	argv := Fill(c.Def.Host.Run, "{label}", c.label(), "{command}", command)
	// Not CommandContext: the process outlives the call that started it and ends
	// with Kill.
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), c.Env...)
	procgroup.Set(cmd)
	cmd.Stderr = c.log()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("host.run: %w", err)
	}
	h := &Held{cmd: cmd, key: make(chan keyResult, 1), done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if rest, ok := strings.CutPrefix(line, KeyLinePrefix); ok && !sent {
				sent = true
				key, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
				h.key <- keyResult{key, derr}
				continue
			}
			fmt.Fprintln(c.log(), line)
		}
		werr := cmd.Wait()
		if !sent {
			h.key <- keyResult{nil, fmt.Errorf("host.run ended before the node printed its key (%v)", exitReason(werr))}
		}
		close(h.done)
	}()
	return h, nil
}

func exitReason(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// NodeKey waits for the node's public key line on the held process's stdout.
func (h *Held) NodeKey(ctx context.Context) ([]byte, error) {
	select {
	case r := <-h.key:
		if r.err != nil {
			return nil, r.err
		}
		if len(r.key) == 0 {
			return nil, errors.New("host.run printed an empty node key")
		}
		return r.key, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Done is closed when the held process has exited and been reaped.
func (h *Held) Done() <-chan struct{} { return h.done }

// Kill ends the held process and everything it started, and reaps it.
func (h *Held) Kill() {
	_ = procgroup.Kill(h.cmd)
	select {
	case <-h.done:
	case <-time.After(killWait):
	}
}
