package agentstep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestDispatchRefusesMissingExecutable (sty_01949949 AC3): a named binding
// whose program is not on this machine's PATH is a hard refusal at dispatch —
// the error names the binding and the program, and nothing runs in-loop instead.
func TestDispatchRefusesMissingExecutable(t *testing.T) {
	docs := fakeDocs{workflow: dispatchWF}
	g, r := newEngine(t, "", docs) // default newRunner: lookupRunner
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		return config.AgentBinding{Role: "agent", Command: "satelle-nonexistent-xyz -p {system}", Tools: "Read"}, true
	})
	_, err := g.DispatchExecutor(context.Background(), workitem.Item{ID: "sty_x", Status: "backlog"}, "plan")
	if err == nil {
		t.Fatal("missing executable must refuse the dispatch")
	}
	msg := err.Error()
	for _, want := range []string{"satelle-nonexistent-xyz", "not executable on this machine", `state "plan"`, "install it or rebind the agent"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if r.got.Payload != "" || r.got.SystemPrompt != "" {
		t.Fatalf("nothing may run when the executable is missing: %+v", r.got)
	}
}

// TestLookupRunnerResolvesOnLocalPath: the same binding resolves once the
// program exists on PATH; in-loop and empty commands never look anything up.
func TestLookupRunnerResolvesOnLocalPath(t *testing.T) {
	bin := t.TempDir()
	exe := filepath.Join(bin, "satelle-fake-agent")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	r, err := lookupRunner("command", "satelle-fake-agent -p {system}")
	if err != nil || r == nil {
		t.Fatalf("resolvable command: runner=%v err=%v", r, err)
	}
	if r, err := lookupRunner("command", "in-loop"); err != nil || r != nil {
		t.Fatalf("in-loop must not look up anything: %v %v", r, err)
	}
	_, err = lookupRunner("command", "satelle-nonexistent-xyz -p {system}")
	var enf *ExecutableNotFoundError
	if !errors.As(err, &enf) || enf.Token != "satelle-nonexistent-xyz" {
		t.Fatalf("want ExecutableNotFoundError naming the token, got %v", err)
	}
}
