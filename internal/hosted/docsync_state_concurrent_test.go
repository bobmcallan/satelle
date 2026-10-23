package hosted

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

const (
	concurrentServer = "https://s.example"
	concurrentRepo   = "/repo"
	saversEnv        = "SATELLE_TEST_HEAD_SAVER"
	saversPathEnv    = "SATELLE_TEST_HEAD_SAVER_STATE"
	savesPerSaver    = 40
)

// saveHeads is one writer's share: savesPerSaver stories, each saved on its own.
func saveHeads(worker string) error {
	for i := 0; i < savesPerSaver; i++ {
		id := fmt.Sprintf("sty_%s_%d", worker, i)
		if err := SaveStoryHead(concurrentServer, "p", concurrentRepo, id, "evt_"+id); err != nil {
			return err
		}
	}
	return nil
}

// TestStoryHeadSaverChild is the writer process: the test binary runs it by
// re-executing itself, and it skips in an ordinary run.
func TestStoryHeadSaverChild(t *testing.T) {
	worker := os.Getenv(saversEnv)
	if worker == "" {
		t.Skip("child of TestStoryHeadsSavedByConcurrentProcesses")
	}
	DocumentSyncStatePathOverride = os.Getenv(saversPathEnv)
	if err := saveHeads(worker); err != nil {
		t.Fatal(err)
	}
}

func wantAllHeads(t *testing.T, workers []string) {
	t.Helper()
	heads, err := LoadWorkstateCursor(concurrentServer, "p", concurrentRepo)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range workers {
		for i := 0; i < savesPerSaver; i++ {
			id := fmt.Sprintf("sty_%s_%d", w, i)
			if heads.StoryHeads[id] != "evt_"+id {
				t.Fatalf("head of %s = %q: another writer's save was lost", id, heads.StoryHeads[id])
			}
		}
	}
	entries, _ := filepath.Glob(DocumentSyncStatePathOverride + ".*.tmp")
	if len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

// sty_9bdf08c4 AC12: two savers of one process do not lose each other's heads.
func TestStoryHeadsSavedByConcurrentGoroutines(t *testing.T) {
	DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "document-sync-state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	var wg sync.WaitGroup
	for _, w := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := saveHeads(w); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	wantAllHeads(t, []string{"a", "b"})
}

// sty_9bdf08c4 AC12: separate satelle processes writing the state file at the same
// time — satelled and a sync, or two repos' syncs — neither fail on a shared temp
// name nor overwrite each other's change.
func TestStoryHeadsSavedByConcurrentProcesses(t *testing.T) {
	if os.Getenv(saversEnv) != "" {
		t.Skip("running as a child")
	}
	path := filepath.Join(t.TempDir(), "document-sync-state.json")
	DocumentSyncStatePathOverride = path
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })
	workers := []string{"p0", "p1", "p2", "p3"}
	cmds := make([]*exec.Cmd, len(workers))
	outs := make([][]byte, len(workers))
	var wg sync.WaitGroup
	for i, w := range workers {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoryHeadSaverChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), saversEnv+"="+w, saversPathEnv+"="+path)
		cmds[i] = cmd
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], _ = cmd.CombinedOutput()
		}()
	}
	wg.Wait()
	for i, cmd := range cmds {
		if !cmd.ProcessState.Success() {
			t.Fatalf("saver %s failed:\n%s", workers[i], outs[i])
		}
	}
	wantAllHeads(t, workers)
}
