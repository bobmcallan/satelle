//go:build !windows

package remotenode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// TestStoryEnvCarriesIdsAndNoMarkers: the story command's process gets the
// story, step and run ids, host.env cannot shadow them, and none of satelle's
// dispatch or gate markers reaches it from the invoking process.
func TestStoryEnvCarriesIdsAndNoMarkers(t *testing.T) {
	markers := []string{config.DispatchAgentEnv, config.DispatchStepEnv, config.DispatchItemEnv, config.SpawnEnv, config.GateStoryEnv}
	for _, k := range markers {
		t.Setenv(k, "leaked")
	}
	out := filepath.Join(t.TempDir(), "env")
	def := storyDef(`sh -c "$1"`, "env > "+out, "{command}")
	cmds := &Commands{Def: def, Env: []string{"KEEP=yes", config.RemoteRunEnv + "=spoof"}}
	h := remotedef.Handoff{Remote: "edge", Story: "sty_x", Step: "in_progress", RunID: "run_abc12345"}

	if err := (StoryRunner{Commands: cmds}).Run(context.Background(), h, remotedef.RenderStoryCommand(&def, "sty_x")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for k, want := range map[string]string{
		config.RemoteStoryEnv: "sty_x", config.RemoteStepEnv: "in_progress", config.RemoteRunEnv: "run_abc12345", "KEEP": "yes",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	for _, k := range markers {
		if v, ok := env[k]; ok {
			t.Errorf("marker %s reached the step's process (%q)", k, v)
		}
	}
}
