//go:build integration

// Cross-process realtime: CLI workspace add → mirror ingest → fragment + SSE.
package tests

import (
	"bufio"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCrossProcessFragmentReflectsCLI asserts the mirror reflects a story after
// a separate CLI process creates it and workspace-addes the snapshot.
func TestCrossProcessFragmentReflectsCLI(t *testing.T) {
	base, repo := serveRepo(t, "8911")

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "Cross-process realtime story", "--body", "made via CLI",
		"--acceptance", "1. the web reflects it",
		"--category", "chore")
	var created struct{ ID string }
	_ = json.Unmarshal([]byte(out), &created)
	if created.ID == "" {
		t.Fatalf("no story id in create output:\n%s", out)
	}
	mustRun(t, testBin, repo, "workspace", "add")

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(httpGet(t, base+"/fragment/stories"), "Cross-process realtime story") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("web /fragment/stories never reflected the CLI-created story after workspace add")
}

// TestCrossProcessSSETrigger asserts SSE fires when a CLI workspace add ingests state.
func TestCrossProcessSSETrigger(t *testing.T) {
	base, repo := serveRepo(t, "8912")
	host := strings.TrimSuffix(base, "/r/"+filepath.Base(repo))

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "SSE trigger seed", "--body", "seed", "--acceptance", "1. ok",
		"--category", "chore")
	var seed struct{ ID string }
	_ = json.Unmarshal([]byte(out), &seed)
	if seed.ID == "" {
		t.Fatalf("no story id:\n%s", out)
	}

	resp, err := http.Get(host + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Kick a push while SSE is open.
	done := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				done <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	mustRun(t, testBin, repo, "workspace", "add")

	select {
	case topic := <-done:
		if topic == "" {
			t.Error("empty SSE topic")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no SSE trigger after workspace add")
	}
}
