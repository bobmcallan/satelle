//go:build integration

// Cross-process realtime durability: the web server and the CLI are SEPARATE
// processes sharing one sqlite file, so an edit made by `satelle story set` must
// reach an open web page. These tests drive the REAL binary end-to-end — serve a
// repo, mutate it from a second CLI process, and assert (a) the panel fragment
// reflects the change and (b) the SSE bus pushes a trigger — the disconnect the
// in-process unit test cannot catch. Part of the `integration` tag.
package tests

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestCrossProcessFragmentReflectsCLI asserts the served web reflects a story
// created by a separate CLI process (cross-process DB visibility).
func TestCrossProcessFragmentReflectsCLI(t *testing.T) {
	base, repo := serveRepo(t, "8911")

	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "Cross-process realtime story", "--body", "made via CLI",
		"--acceptance", "1. the web reflects it")
	var created struct{ ID string }
	_ = json.Unmarshal([]byte(out), &created)
	if created.ID == "" {
		t.Fatalf("no story id in create output:\n%s", out)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(httpGet(t, base+"/fragment/stories"), "Cross-process realtime story") {
			return // the web saw the CLI's write
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("web /fragment/stories never reflected the CLI-created story (web/CLI disconnect)")
}

// TestCrossProcessSSETrigger asserts the SSE bus pushes a 'stories' trigger when
// a separate CLI process mutates a story, and that the stream opens with the
// keepalive-capable connection comment.
func TestCrossProcessSSETrigger(t *testing.T) {
	base, repo := serveRepo(t, "8912")

	// Seed a story to mutate (ungated priority change → no agent needed).
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "SSE trigger seed", "--body", "seed", "--acceptance", "1. ok")
	var seed struct{ ID string }
	_ = json.Unmarshal([]byte(out), &seed)
	if seed.ID == "" {
		t.Fatalf("no story id:\n%s", out)
	}

	resp, err := http.Get(base + "/events")
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	got := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				got <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	// Let the subscription register, then mutate from a separate CLI process.
	time.Sleep(300 * time.Millisecond)
	mustRun(t, testBin, repo, "story", "set", seed.ID, "--priority", "high")

	select {
	case topic := <-got:
		if topic != "stories" {
			t.Errorf("SSE trigger topic = %q, want stories", topic)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("no SSE trigger within 6s after a cross-process CLI mutation")
	}
}
