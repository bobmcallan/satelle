//go:build !windows

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotechan/relaytest"
	"github.com/bobmcallan/satelle/internal/remotenode"
)

type nodeTestStore struct{ cred hosted.Credential }

func (m *nodeTestStore) Load(string) (hosted.Credential, error) { return m.cred, nil }
func (m *nodeTestStore) Save(c hosted.Credential) error         { m.cred = c; return nil }
func (m *nodeTestStore) Delete(string) error                    { return nil }

// nodeLog is the node's stderr, readable while the node still writes it.
type nodeLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *nodeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *nodeLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// nodeDefinition is the files a controller packs into a node's substrate: the
// directories remotenode.SubstrateDirs names, a one-lane workflow, one binding
// that runs the fake agent, one skill and one principle.
var nodeDefinition = map[string]string{
	"workflows/done.toml": `[meta]
name = "done"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "The node's declaration of done."

["*"]
obligations = ["raised", "coded", "closed"]
`,
	"workflows/step.toml": `[meta]
name = "step"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "The node's step catalogue."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "worker"
skills = ["remote-work"]
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`,
	"workflows/agents.toml": "[worker]\nrole = \"agent\"\ncommand = \"fake-agent --system {system} --tools {tools}\"\ntools = \"read_file\"\n",
	"skills/remote-work.md": `---
name: remote-work
type: skill
scope: project
tags: [type:skill]
description: The rubric the node's worker follows when it performs the coded step.
---

# Remote work

Implement the work item you are given.
`,
	"principles/remote-conduct.md": `---
name: remote-conduct
type: principle
tags: [principles:session]
description: How an agent on this node conducts itself.
---

# Remote conduct

Work only inside the story you were handed.
`,
}

// `satelle node join` is the node mode that stays in the binary: handed the launch
// contract (--root, --relay, --session, --token) it prints its key line, enrolls on
// the relay, unpacks the PackSubstrate-built substrate its Setup carries, reports
// ready and answers an agent job. The controller end is remotenode.Attach.
func TestNodeJoinServesSetupAndAgentJob(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the fake agent is a shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "fake-agent"), []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The node is a fresh machine: nothing of this repo's, nor of the developer's.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("SATELLE_CONFIG", "")
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	t.Setenv(config.SessionEnv, "")
	// join changes into the node root; the cleanup puts the working directory back.
	t.Chdir(t.TempDir())

	relay := relaytest.New(t)
	server := strings.TrimSuffix(relay.URL(), "/relay")
	client := hosted.NewClient(server, &nodeTestStore{cred: hosted.Credential{ServerURL: server, AccessToken: "access-1"}}, nil)
	prev := nodeChannelOptions
	nodeChannelOptions = []remotechan.Option{remotechan.WithHTTPClient(relay.Client())}
	t.Cleanup(func() { nodeChannelOptions = prev })

	defDir := t.TempDir()
	for rel, body := range nodeDefinition {
		p := filepath.Join(defDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	substrate, err := remotenode.PackSubstrate(defDir)
	if err != nil {
		t.Fatal(err)
	}

	nodeRoot := filepath.Join(t.TempDir(), "satelle-node")
	log := &nodeLog{}
	joined := make(chan error, 1)
	launch := remotechan.LauncherFunc(func(_ context.Context, spec remotechan.LaunchSpec) ([]byte, error) {
		pr, pw := io.Pipe()
		root := NewRootCmd()
		resetFlagState(root)
		root.SetOut(pw)
		root.SetErr(log)
		root.SetArgs([]string{"node", "join", "--root", nodeRoot, "--relay", spec.RelayURL, "--session", spec.Session, "--token", spec.Token})
		go func() {
			err := executeRoot(root)
			_ = pw.CloseWithError(err)
			joined <- err
		}()
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), remotenode.KeyLinePrefix); ok {
				go func() { _, _ = io.Copy(io.Discard, pr) }()
				return base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
			}
		}
		return nil, errors.New("node join printed no key line:\n" + log.String())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sess, err := remotechan.Open(ctx, client, relay.URL(), launch, remotechan.WithHTTPClient(relay.Client()))
	if err != nil {
		t.Fatalf("Open: %v\nnode log:\n%s", err, log)
	}
	setup := remotenode.Setup{Substrate: substrate, Allow: []string{"satelle.test"}}
	run, err := remotenode.Attach(ctx, sess, setup, 60*time.Second)
	if err != nil {
		t.Fatalf("Attach: %v\nnode log:\n%s", err, log)
	}

	// Ready: the node resolved the substrate's own binding and workflow lane.
	if run.Report.Error != "" || run.Report.Version == "" {
		t.Errorf("report = %+v", run.Report)
	}
	if !reflect.DeepEqual(run.Report.Bindings, []string{"worker"}) || !reflect.DeepEqual(run.Report.Workflows, []string{"*"}) {
		t.Errorf("report bindings/workflows = %v / %v", run.Report.Bindings, run.Report.Workflows)
	}
	if !reflect.DeepEqual(run.Report.Allow, setup.Allow) {
		t.Errorf("report allow = %v, want %v", run.Report.Allow, setup.Allow)
	}

	// Unpacked: init made the node's data dir and the substrate is the definition's.
	for rel, want := range nodeDefinition {
		got, err := os.ReadFile(filepath.Join(nodeRoot, ".satelle", filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("the node lacks %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("the node's %s is not the definition's", rel)
		}
	}
	// Nothing secret was written to the node.
	if _, err := os.Stat(filepath.Join(nodeRoot, ".satelle", "config", ".env")); err == nil {
		t.Error("a .env was written to the node")
	}

	// The agent job: the node's binding runs the fake agent, which echoes its message.
	body, err := run.Request(ctx, remotenode.AgentJob, remotenode.AgentRequest{Text: "hello node"})
	if err != nil {
		t.Fatalf("agent job: %v\nnode log:\n%s", err, log)
	}
	rep, err := remotenode.DecodeAgentReply(body)
	if err != nil || !strings.Contains(rep.Text, "hello node") {
		t.Errorf("agent reply = %+v, %v", rep, err)
	}

	// Closing the channel ends the node's join cleanly.
	if err := run.End(); err != nil {
		t.Errorf("End: %v", err)
	}
	select {
	case err := <-joined:
		if err != nil {
			t.Errorf("node join ended with %v\nnode log:\n%s", err, log)
		}
	case <-time.After(30 * time.Second):
		t.Error("node join did not end after the controller closed the channel")
	}
}
