//go:build !windows

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/filebundle"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotechan/relaytest"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/remotenode/devtest"
	"github.com/bobmcallan/satelle/internal/remotenode/gittest"
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

// nodeDefinition is the substrate files of a node's definition: the directories
// remotenode.SubstrateDirs names, a one-lane workflow, one binding that runs the
// fake agent, one skill and one principle. The definition's remote.toml is the
// one satelle-server accepts (remotedef's testdata).
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

// nodeRig is one node machine: its environment, the relay and the satelle.dev fake
// it talks to.
type nodeRig struct {
	t        *testing.T
	relay    *relaytest.Relay
	log      *nodeLog
	nodeRoot string
	marker   string // the fake agent appends a line here each time it runs
	repo     *gittest.Server
	bundle   map[string]string
}

// newNodeRig sets the environment of a fresh machine with a fake agent on its PATH
// and the seams pointing at in-process fakes. The definition it will serve is
// nodeDefinition plus the satelle-server fixture as its remote.toml.
func newNodeRig(t *testing.T) *nodeRig {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the fake agent is a shell script")
	}
	r := &nodeRig{t: t, log: &nodeLog{}, relay: relaytest.New(t), repo: gittest.New(t)}
	// Read before the working directory changes: it is the fixture satelle-server
	// accepts, committed with the remotedef package.
	remoteToml, err := os.ReadFile(filepath.Join("..", "remotedef", "testdata", "remote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	r.marker = filepath.Join(t.TempDir(), "agent-ran")
	script := "#!/bin/sh\necho ran >> " + r.marker + "\necho \"TOKEN=$CLAUDE_CODE_OAUTH_TOKEN\"\ncat\n"
	if err := os.WriteFile(filepath.Join(bin, "fake-agent"), []byte(script), 0o755); err != nil {
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
	r.nodeRoot = filepath.Join(t.TempDir(), "satelle-node")

	prevOpts, prevServer, prevHTTP, prevEnv := nodeChannelOptions, nodeServer, nodeHTTP, nodeFetchEnv
	nodeChannelOptions = []remotechan.Option{remotechan.WithHTTPClient(r.relay.Client())}
	// The fixture's repo is on github.com; the node's git is told the local server is it.
	nodeFetchEnv = append(append([]string(nil), r.repo.Env...),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=url."+r.repo.URL+".insteadOf",
		"GIT_CONFIG_VALUE_0=https://github.com/acme/widgets.git")
	t.Cleanup(func() {
		nodeChannelOptions, nodeServer, nodeHTTP, nodeFetchEnv = prevOpts, prevServer, prevHTTP, prevEnv
	})

	r.bundle = map[string]string{"remote.toml": string(remoteToml)}
	for rel, body := range nodeDefinition {
		r.bundle[rel] = body
	}
	return r
}

// encode is the stored bundle: the rig's files as a format-1 bundle, in path order.
func (r *nodeRig) encode() []byte {
	r.t.Helper()
	paths := make([]string, 0, len(r.bundle))
	for p := range r.bundle {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	entries := make([]filebundle.Entry, len(paths))
	for i, p := range paths {
		entries[i] = filebundle.Entry{Path: p, Content: r.bundle[p]}
	}
	b, err := filebundle.Encode(entries)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

// serve points the node at a satelle.dev fake for node id.
func (r *nodeRig) serve(id, token string, bundle []byte) *devtest.Node {
	r.t.Helper()
	fake := devtest.NewNode(r.t, id, token, bundle, "v7")
	fake.Secrets["CLAUDE_CODE_OAUTH_TOKEN"] = devtest.SecretValue
	nodeServer = func() string { return fake.URL }
	nodeHTTP = fake.Client()
	return fake
}

// join runs `satelle node join` in this process and returns its error.
func (r *nodeRig) join(stdout io.Writer, relayURL, session, token string) error {
	root := NewRootCmd()
	resetFlagState(root)
	root.SetOut(stdout)
	root.SetErr(r.log)
	root.SetArgs([]string{"node", "join", "--root", r.nodeRoot, "--relay", relayURL, "--session", session, "--token", token})
	return executeRoot(root)
}

// joinNow runs join to its end with nothing reading the key line.
func (r *nodeRig) joinNow(relayURL, session, token string) error {
	return r.join(io.Discard, relayURL, session, token)
}

// launcher plays satelle-server's launcher: once Open has issued the session and
// the token it starts the satelle.dev fake for that node, keyed by the session as
// the real server keys it, and starts `satelle node join` against it.
func (r *nodeRig) launcher(fakeCh chan<- *devtest.Node, joined chan<- error) remotechan.Launcher {
	return remotechan.LauncherFunc(func(_ context.Context, spec remotechan.LaunchSpec) ([]byte, error) {
		fakeCh <- r.serve(spec.Session, spec.Token, r.encode())
		pr, pw := io.Pipe()
		go func() {
			err := r.join(pw, spec.RelayURL, spec.Session, spec.Token)
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
		return nil, errors.New("node join printed no key line:\n" + r.log.String())
	})
}

// open brings a node up through the relay and returns the controller's run.
func (r *nodeRig) open(t *testing.T) (run *remotenode.Run, fake *devtest.Node, joined chan error, spec remotechan.LaunchSpec) {
	t.Helper()
	fakeCh := make(chan *devtest.Node, 1)
	joined = make(chan error, 1)
	client := hosted.NewClient("https://satelle.test", &nodeTestStore{cred: hosted.Credential{ServerURL: "https://satelle.test", AccessToken: "access-1"}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	launch := r.launcher(fakeCh, joined)
	var got remotechan.LaunchSpec
	sess, err := remotechan.Open(ctx, client, r.relay.URL(), remotechan.LauncherFunc(func(c context.Context, s remotechan.LaunchSpec) ([]byte, error) {
		got = s
		return launch.Launch(c, s)
	}), remotechan.WithHTTPClient(r.relay.Client()))
	if err != nil {
		t.Fatalf("Open: %v\nnode log:\n%s", err, r.log)
	}
	run, err = remotenode.Attach(ctx, sess, 60*time.Second)
	if err != nil {
		t.Fatalf("Attach: %v\nnode log:\n%s", err, r.log)
	}
	return run, <-fakeCh, joined, got
}

// files is every file under dir, by slash path.
func nodeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func waitJoin(t *testing.T, joined <-chan error) error {
	t.Helper()
	select {
	case err := <-joined:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("node join did not end")
		return nil
	}
}

// `satelle node join` is the node mode that stays in the binary: handed the launch
// contract (--root, --relay, --session, --token) it prints its key line, fetches
// its definition from satelle.dev with its node token, installs and reindexes it,
// joins the relay with the same token, reports ready, answers an agent job whose
// secrets it read from satelle.dev, and ends itself through satelle.dev. The
// controller end is remotenode.Attach.
func TestNodeJoinRunsFromTheDefinitionItFetches(t *testing.T) {
	r := newNodeRig(t)
	run, fake, joined, spec := r.open(t)

	// AC1: the definition came from the fake with the node bearer, under the
	// session as node id, and the report carries the version header.
	calls := fake.Calls()
	if len(calls) == 0 || calls[0].Method != http.MethodGet || calls[0].Path != "/api/v1/remote-nodes/"+spec.Session+"/definition" ||
		calls[0].Auth != "Bearer "+spec.Token {
		t.Fatalf("first call = %+v, want the authenticated definition fetch", calls)
	}
	rep := run.Report
	if rep.Error != "" || rep.Version == "" || rep.DefinitionVersion != "v7" {
		t.Errorf("report = %+v", rep)
	}
	if !reflect.DeepEqual(rep.Bindings, []string{"worker"}) || !reflect.DeepEqual(rep.Workflows, []string{"*"}) {
		t.Errorf("report bindings/workflows = %v / %v", rep.Bindings, rep.Workflows)
	}
	// The report's allowlist and repos are what remote.toml declares.
	if want := []string{"api.example.com", "*.example.org"}; !reflect.DeepEqual(rep.Allow, want) {
		t.Errorf("report allow = %v, want %v", rep.Allow, want)
	}
	if len(rep.Repos) != 1 || rep.Repos[0].Name != "widgets" || rep.Repos[0].Commit != r.repo.Head || rep.Repos[0].Ref != "main" {
		t.Errorf("report repos = %+v, want widgets at %s", rep.Repos, r.repo.Head)
	}

	// The installed substrate is the served substrate, and remote.toml is not installed.
	dataDir := filepath.Join(r.nodeRoot, ".satelle")
	for rel, want := range nodeDefinition {
		got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("the node lacks %s: %v", rel, err)
		} else if string(got) != want {
			t.Errorf("the node's %s is not the definition's", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "remote.toml")); err == nil {
		t.Error("remote.toml was installed on the node")
	}
	// Nothing secret was written to the node.
	if _, err := os.Stat(filepath.Join(dataDir, "config", ".env")); err == nil {
		t.Error("a .env was written to the node")
	}

	// AC9: the relay saw the node token as the remote's bearer.
	if got := r.relay.RemoteBearers(); !reflect.DeepEqual(got, []string{spec.Token}) {
		t.Errorf("relay remote bearers = %v, want [the node token]", got)
	}

	// AC5: the agent job reads the declared secret from satelle.dev with the node
	// bearer and the agent sees it in its environment.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	body, err := run.Request(ctx, remotenode.AgentJob, remotenode.AgentRequest{Text: "hello node"})
	if err != nil {
		t.Fatalf("agent job: %v\nnode log:\n%s", err, r.log)
	}
	reply, err := remotenode.DecodeAgentReply(body)
	if err != nil || !strings.Contains(reply.Text, "hello node") || !strings.Contains(reply.Text, "TOKEN="+devtest.SecretValue) {
		t.Errorf("agent reply = %+v, %v", reply, err)
	}
	var secretCalls []devtest.Call
	for _, c := range fake.Calls() {
		if strings.Contains(c.Path, "/secrets/") {
			secretCalls = append(secretCalls, c)
		}
	}
	if len(secretCalls) != 1 || secretCalls[0].Method != http.MethodPost || secretCalls[0].Auth != "Bearer "+spec.Token ||
		secretCalls[0].Path != "/api/v1/remote-nodes/"+spec.Session+"/secrets/CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("secret calls = %+v", secretCalls)
	}

	// Closing the channel ends the node's join cleanly, and the node ends itself
	// through satelle.dev exactly once.
	if err := run.End(); err != nil {
		t.Errorf("End: %v", err)
	}
	if err := waitJoin(t, joined); err != nil {
		t.Errorf("node join ended with %v\nnode log:\n%s", err, r.log)
	}
	if n := fake.Count(http.MethodPost, "/end"); n != 1 {
		t.Errorf("end calls = %d, want 1", n)
	}
	if n := fake.Count(http.MethodGet, "/definition"); n != 1 {
		t.Errorf("definition fetches = %d, want 1", n)
	}

	// The secret value is in no file of the node and not in its log.
	for rel, body := range nodeFiles(t, r.nodeRoot) {
		if strings.Contains(body, devtest.SecretValue) {
			t.Errorf("the secret value was written to %s", rel)
		}
	}
	if strings.Contains(r.log.String(), devtest.SecretValue) {
		t.Error("the secret value is in the node's log")
	}
}

// AC8: a node whose token is revoked mid-run stops with a message saying so and
// naming the route; nothing further is asked of satelle.dev, the agent never runs
// and at most one end call is made.
func TestNodeJoinStopsWhenItsTokenIsRevoked(t *testing.T) {
	r := newNodeRig(t)
	run, fake, joined, _ := r.open(t)
	fake.Revoke()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, rerr := run.Request(ctx, remotenode.AgentJob, remotenode.AgentRequest{Text: "work"})
	if rerr == nil || !strings.Contains(rerr.Error(), "refused or revoked") {
		t.Errorf("agent job error = %v, want the token refused or revoked", rerr)
	}

	err := waitJoin(t, joined)
	if err == nil || !strings.Contains(err.Error(), "node token was refused or revoked") ||
		!strings.Contains(err.Error(), "POST /api/v1/remote-nodes/") || !strings.Contains(err.Error(), "/secrets/CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("node join ended with %v, want the token refused naming the secrets route", err)
	}
	if _, serr := os.Stat(r.marker); serr == nil {
		t.Error("the agent ran after the token was refused")
	}
	if n := fake.Count(http.MethodGet, "/definition"); n != 1 {
		t.Errorf("definition fetches = %d, want only the one before the 401", n)
	}
	if n := fake.Count(http.MethodPost, "/secrets/CLAUDE_CODE_OAUTH_TOKEN"); n != 1 {
		t.Errorf("secret reads = %d, want only the refused one", n)
	}
	if n := fake.Count(http.MethodPost, "/end"); n > 1 {
		t.Errorf("end calls = %d, want at most 1", n)
	}
}

// AC5: a key satelle.dev refuses (not declared or not granted) stops the job
// before the agent starts, with a refusal naming the key; the node stays up.
func TestNodeJoinRefusedSecretStopsTheJobBeforeTheAgentStarts(t *testing.T) {
	r := newNodeRig(t)
	run, fake, joined, _ := r.open(t)
	delete(fake.Secrets, "CLAUDE_CODE_OAUTH_TOKEN")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := run.Request(ctx, remotenode.AgentJob, remotenode.AgentRequest{Text: "work"})
	if err == nil || !strings.Contains(err.Error(), "secret CLAUDE_CODE_OAUTH_TOKEN refused") {
		t.Fatalf("agent job error = %v, want a refusal naming the key", err)
	}
	if _, serr := os.Stat(r.marker); serr == nil {
		t.Error("the agent started after a secret was refused")
	}
	if err := run.End(); err != nil {
		t.Errorf("End: %v", err)
	}
	if jerr := waitJoin(t, joined); jerr != nil {
		t.Errorf("a refused secret must not stop the node: %v", jerr)
	}
}

// AC3 and AC7: a definition that does not check out stops the join naming the
// cause, installs nothing, and the node still ends itself exactly once.
func TestNodeJoinWithABadDefinitionInstallsNothingAndEndsOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(r *nodeRig, fake *devtest.Node)
		serve func(r *nodeRig) []byte
		want  string
	}{
		{"digest mismatch", func(_ *nodeRig, f *devtest.Node) { f.ETag = strings.Repeat("0", 64) }, nil, "does not match its ETag"},
		{"another format", nil, func(*nodeRig) []byte { return []byte(`{"format":2,"files":[]}`) }, "unknown bundle format 2"},
		{"a path outside the roots", nil, func(r *nodeRig) []byte { r.bundle["notes/x.md"] = "x"; return r.encode() }, "outside the bundle's roots"},
		{"malformed remote.toml", nil, func(r *nodeRig) []byte { r.bundle["remote.toml"] = "[[secret\n"; return r.encode() }, "remote.toml"},
		{"wrong-typed field", nil, func(r *nodeRig) []byte { r.bundle["remote.toml"] = "[network]\nallow = \"x\"\n"; return r.encode() }, "remote.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNodeRig(t)
			bundle := r.encode()
			if tc.serve != nil {
				bundle = tc.serve(r)
			}
			fake := r.serve("nod_a", "node-token", bundle)
			if tc.setup != nil {
				tc.setup(r, fake)
			}

			err := r.joinNow(r.relay.URL(), "nod_a", "node-token")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("join = %v, want an error containing %q", err, tc.want)
			}
			if n := fake.Count(http.MethodPost, "/end"); n != 1 {
				t.Errorf("end calls = %d, want 1", n)
			}
			if _, serr := os.Stat(filepath.Join(r.nodeRoot, ".satelle", "skills", "remote-work.md")); serr == nil {
				t.Error("a refused definition installed its skills")
			}
			if r.relay.Attempts() != 0 {
				t.Error("the node joined the relay before it had a definition")
			}
		})
	}
}

// AC7: a definition fetch that fails outright (the server is not reachable, or does
// not know the node) still ends the node once; AC10: the fake keys every route by
// the node id, so a node started with another --session than the fake's gets a 404.
func TestNodeJoinWithAnotherSessionThanTheServersIDIsNotFound(t *testing.T) {
	r := newNodeRig(t)
	fake := r.serve("nod_a", "node-token", r.encode())

	err := r.joinNow(r.relay.URL(), "nod_b", "node-token")
	if err == nil || !strings.Contains(err.Error(), "definition") {
		t.Fatalf("join = %v, want the definition fetch to fail", err)
	}
	calls := fake.Calls()
	if len(calls) == 0 || calls[0].Path != "/api/v1/remote-nodes/nod_b/definition" || calls[0].Status != http.StatusNotFound {
		t.Errorf("calls = %+v, want a 404 for the definition fetch under nod_b", calls)
	}
	if fake.Count(http.MethodGet, "/definition") != 0 {
		t.Error("the fake served a definition under another id")
	}
}

// A bad token at satelle.dev stops the join at the definition fetch with the
// token's refusal, and makes no further calls beyond the one end.
func TestNodeJoinWithABadTokenIsRefused(t *testing.T) {
	r := newNodeRig(t)
	fake := r.serve("nod_a", "node-token", r.encode())

	err := r.joinNow(r.relay.URL(), "nod_a", "wrong-token")
	if err == nil || !strings.Contains(err.Error(), "node token was refused or revoked") || !strings.Contains(err.Error(), "/definition") {
		t.Fatalf("join = %v, want the token refused naming the definition route", err)
	}
	if calls := fake.Calls(); len(calls) != 2 || calls[1].Path != "/api/v1/remote-nodes/nod_a/end" {
		t.Errorf("calls = %+v, want the refused definition and one end", calls)
	}
}

// AC9: a relay that refuses the node token (401) is reported as the token's
// refusal, and the node still ends itself once.
func TestNodeJoinRelayRefusalIsTheTokensRefusal(t *testing.T) {
	r := newNodeRig(t)
	fake := r.serve("nod_a", "node-token", r.encode())
	r.relay.RejectRemoteBearers(1)

	err := r.joinNow(r.relay.URL(), "nod_a", "node-token")
	if err == nil || !strings.Contains(err.Error(), "node token was refused or revoked") || !strings.Contains(err.Error(), "relay") {
		t.Fatalf("join = %v, want the token refused by the relay", err)
	}
	if got := r.relay.RemoteBearers(); !reflect.DeepEqual(got, []string{"node-token"}) {
		t.Errorf("relay remote bearers = %v", got)
	}
	if n := fake.Count(http.MethodPost, "/end"); n != 1 {
		t.Errorf("end calls = %d, want 1", n)
	}
}
