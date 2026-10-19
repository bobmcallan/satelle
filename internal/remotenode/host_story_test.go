//go:build !windows

package remotenode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/remotedef"
)

// storyDef is a definition whose host.run is the given shell script (its $0 is
// "fakehost", followed by the placeholders) and whose story command is storyCmd.
func storyDef(script, storyCmd string, tail ...string) remotedef.Definition {
	var def remotedef.Definition
	def.Identity.Label = "edge-label"
	def.Host.Run = append([]string{"sh", "-c", script, "fakehost"}, tail...)
	def.Host.StoryCommand = storyCmd
	return def
}

// TestStoryRunnerArgvThroughHostRunArgv: the argv the host receives is exactly
// what Definition.HostRunArgv builds for the rendered story command.
func TestStoryRunnerArgvThroughHostRunArgv(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv")
	def := storyDef(`printf '%s\n' "$@" > `+log, "work {story}", "{label}", "--", "{command}")
	command := remotedef.RenderStoryCommand(&def, "sty_x")
	if command != "work sty_x" {
		t.Fatalf("rendered story command = %q", command)
	}

	r := StoryRunner{Commands: &Commands{Def: def}}
	if err := r.Run(context.Background(), remotedef.Handoff{Story: "sty_x"}, command); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	want := []string{"edge-label", "--", "work sty_x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("host argv = %q, want %q", got, want)
	}
	if built := def.HostRunArgv(command); !reflect.DeepEqual(got, built[4:]) {
		t.Errorf("host argv = %q, but HostRunArgv builds %q", got, built[4:])
	}
}

// TestStoryRunnerCancelKillsGroup: cancelling a running story command kills the
// command's whole process group, so a child it started does not survive.
func TestStoryRunnerCancelKillsGroup(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	// $1 is the rendered command, which the host runs under its own shell.
	def := storyDef(`sh -c "$1"`, "sleep 60 & echo $! > "+pidfile+"; wait", "{command}")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	r := StoryRunner{Commands: &Commands{Def: def}}
	go func() {
		errc <- r.Run(ctx, remotedef.Handoff{Story: "sty_x"}, remotedef.RenderStoryCommand(&def, "sty_x"))
	}()

	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(pidfile); err == nil {
			if p, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil {
				pid = p
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the story command never wrote its child's pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-errc:
		if err == nil {
			t.Error("a cancelled story command must return an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %d survived the cancel", pid)
		}
	}
}
