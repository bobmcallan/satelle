package remotenode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/logsread"
	"github.com/bobmcallan/satelle/internal/testutil"
)

func writeNodeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The node answers with this story's dispatch logs at or after since — not
// another story's, not earlier ones — and with its own clock.
func TestNodeStoryReportReturnsOnlyTheStorysLogsSinceTheStart(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	var process config.Config
	dispatch := filepath.Join(process.ResolveLogsDir(root), "dispatch")
	since := int64(2_000)
	mk := func(agent, story string, nano int64) string {
		p := filepath.Join(dispatch, logsread.FormatDispatchName(agent, story, nano))
		writeNodeFile(t, p, "log")
		return p
	}
	mk("planner", "sty_x", since-1) // before the start
	mk("planner", "sty_y", since+5) // another story
	atStart := mk("planner", "sty_x", since)
	after := mk("reviewer", "sty_x", since+10)

	now := time.Unix(0, 12345)
	rep, err := NodeStoryReport(process, root, StoryReportRequest{Story: "sty_x", Since: since}, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range rep.Sessions {
		got[p] = true
	}
	if len(rep.Sessions) != 2 || !got[atStart] || !got[after] {
		t.Errorf("sessions = %v, want exactly [%s %s]", rep.Sessions, atStart, after)
	}
	if rep.Now != 12345 {
		t.Errorf("now = %d, want the node's clock 12345", rep.Now)
	}
}

// The reply carries config.ProcessHashes, so an edit under a substrate root or
// to the constitution shows in a later report.
func TestNodeStoryReportCarriesTheProcessHashes(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	var process config.Config
	skill := filepath.Join(process.ResolveAuthoredDirs(root)["skills"], "s.md")
	writeNodeFile(t, skill, "one")
	writeNodeFile(t, process.ResolveConstitution(root), "c")

	req := StoryReportRequest{Story: "sty_x"}
	first, err := NodeStoryReport(process, root, req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Hashes, config.ProcessHashes(process, root)) {
		t.Errorf("hashes = %v, want config.ProcessHashes", first.Hashes)
	}
	writeNodeFile(t, skill, "two")
	writeNodeFile(t, process.ResolveConstitution(root), "c2")
	second, _ := NodeStoryReport(process, root, req, time.Now())
	if got := len(config.DiffTreeHashes(first.Hashes, second.Hashes)); got != 2 {
		t.Errorf("the diff names %d paths, want the skill and the constitution", got)
	}
}

func TestStoryReportHandlerDecodesTheRequest(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	h := StoryReportHandler(func() (config.Config, string, error) { return config.Config{}, root, nil })
	body, err := h(context.Background(), json.RawMessage(`{"story":"sty_x","since":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var rep StoryReportReply
	if err := json.Unmarshal(body, &rep); err != nil || rep.Now == 0 || rep.Sessions == nil {
		t.Errorf("reply = %s (%v), want a clock and an empty, non-null session list", body, err)
	}
}
