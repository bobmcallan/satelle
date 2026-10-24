package wfdot

import (
	"strings"
	"testing"
)

// The step key half of the remote-run wait: `remote_wait = { remote, on }`
// parses onto the Step and the assembled State, its mis-authorings are refused
// by name, and every mapped outcome must be an edge the route declares. The
// fixture steps and statuses are data; nothing here is the binary's vocabulary.

const remoteWaitDone = `["*"]
obligations = ["raised", "coded", "opened", "closed"]
park = { state = "blocked", gate = "park-gate" }
cancel = { state = "cancelled", gate = "cancel-gate" }
recover = { step = "in_progress", from = ["review"] }
`

func remoteWaitSteps(waitLine string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
skills = ["coder"]
requires = ["raised"]

[opened]
status = "review"
requires = ["coded"]
` + waitLine + `
[closed]
status = "done"
terminal = true
requires = ["opened"]
`
}

func TestRemoteWaitParsesOntoStepAndSpec(t *testing.T) {
	body := remoteWaitSteps(`remote_wait = { remote = "pr-team", on = { accept = "done", reject = "in_progress", stopped = "blocked" } }` + "\n")
	cat, err := ParseSteps(body)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "opened")
	if st.RemoteWait == nil || st.RemoteWait.Remote != "pr-team" || st.RemoteWait.On["reject"] != "in_progress" {
		t.Fatalf("Step.RemoteWait = %+v", st.RemoteWait)
	}
	if got := stepProviding(t, cat, "coded").RemoteWait; got != nil {
		t.Errorf("a step with no remote_wait carries none, got %+v", got)
	}
	spec, err := ParseRoute(remoteWaitDone, body, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	rw, ok := spec.RemoteWaitOf("review")
	if !ok || rw.Remote != "pr-team" || rw.On["accept"] != "done" || rw.On["stopped"] != "blocked" {
		t.Errorf("Spec.RemoteWaitOf(review) = %+v, %v", rw, ok)
	}
	if _, ok := spec.RemoteWaitOf("in_progress"); ok {
		t.Error("a step with no remote_wait waits on nothing")
	}
	if _, ok := spec.RemoteWaitOf("nowhere"); ok {
		t.Error("an unknown status waits on nothing")
	}
}

func TestRemoteWaitRefusesUndeclaredEdge(t *testing.T) {
	body := remoteWaitSteps(`remote_wait = { remote = "pr-team", on = { accept = "release" } }` + "\n")
	_, err := ParseRoute(remoteWaitDone, body, "feature", nil)
	if err == nil {
		t.Fatal("an outcome mapped to a status the route has no edge to must be refused")
	}
	for _, want := range []string{`"review"`, `"accept"`, `"release"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s: %v", want, err)
		}
	}
}

// The check runs once every edge exists: a recover edge, the park edge and the
// spine successor are all legal targets from the waiting step.
func TestRemoteWaitAcceptsRecoverParkSpine(t *testing.T) {
	body := remoteWaitSteps(`remote_wait = { remote = "pr-team", on = { accept = "done", reject = "in_progress", stopped = "blocked", withdrawn = "cancelled" } }` + "\n")
	if _, err := ParseRoute(remoteWaitDone, body, "feature", nil); err != nil {
		t.Fatalf("spine, recover, park and cancel targets are declared edges: %v", err)
	}
}

func TestRemoteWaitRefusesEmpty(t *testing.T) {
	for name, line := range map[string]string{
		"no remote":    `remote_wait = { remote = " ", on = { accept = "done" } }`,
		"no outcomes":  `remote_wait = { remote = "pr-team", on = {} }`,
		"blank target": `remote_wait = { remote = "pr-team", on = { accept = " " } }`,
		"blank key":    `remote_wait = { remote = "pr-team", on = { " " = "done" } }`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSteps(remoteWaitSteps(line + "\n")); err == nil {
				t.Fatal("a half-authored remote_wait must be refused")
			} else if !strings.Contains(err.Error(), "remote_wait") {
				t.Errorf("error should name remote_wait: %v", err)
			}
		})
	}
}

func TestRemoteWaitRefusesWithAgent(t *testing.T) {
	body := strings.Replace(
		remoteWaitSteps(`remote_wait = { remote = "pr-team", on = { accept = "done" } }`+"\n"),
		"status = \"review\"\n", "status = \"review\"\nagent = \"coder\"\n", 1)
	_, err := ParseSteps(body)
	if err == nil || !strings.Contains(err.Error(), "no performer") {
		t.Fatalf("a waiting step that names a performer must be refused, got %v", err)
	}
}
