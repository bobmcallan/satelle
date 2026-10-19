package cli

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/release"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotechan/relaytest"
	"github.com/bobmcallan/satelle/internal/remotenode/gittest"
)

type e2eStore struct{ cred hosted.Credential }

func (m *e2eStore) Load(string) (hosted.Credential, error) { return m.cred, nil }
func (m *e2eStore) Save(c hosted.Credential) error         { m.cred = c; return nil }
func (m *e2eStore) Delete(string) error                    { return nil }

// remoteRig is a developer repo with one remote definition, a stand-in relay,
// a fake release server and a fake host. The host commands are shell scripts; the
// run command executes the bootstrap script for real, so the pinned version is
// installed by the real scripts/install.sh from the fake release server (the
// "release" is this test binary behind a wrapper script) and the real
// `satelle remote join` runs as a child process in a node home that starts empty.
type remoteRig struct {
	t         *testing.T
	repo      string
	server    string // hosted server base the relay lives under
	relay     *relaytest.Relay
	git       *gittest.Server
	nodeHome  string
	nodeRoot  string
	hostLog   string
	installer string
}

const pinnedVersion = "1.2.3"

func newRemoteRig(t *testing.T, lifecycle, workerCommand string) *remoteRig {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the fake node installs through install.sh")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("install.sh supports amd64 and arm64")
	}
	for _, tool := range []string{"sh", "curl", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	r := &remoteRig{t: t, relay: relaytest.New(t), git: gittest.New(t)}
	installer, err := filepath.Abs(filepath.Join("..", "..", "scripts", "install.sh")) // before the chdir
	if err != nil {
		t.Fatal(err)
	}
	r.installer = installer
	r.repo = tempRepo(t)
	t.Chdir(r.repo)
	r.server = strings.TrimSuffix(r.relay.URL(), "/relay")
	r.nodeHome = t.TempDir()
	r.nodeRoot = filepath.Join(r.nodeHome, "satelle-node")
	r.hostLog = filepath.Join(t.TempDir(), "host.log")

	// The sign-in and the relay's certificate, as the node's process will see them.
	client := hosted.NewClient(r.server, &e2eStore{cred: hosted.Credential{ServerURL: r.server, AccessToken: "access-1"}}, nil)
	prevClient, prevOpts := remoteHostedClient, remoteChannelOptions
	remoteHostedClient = func(string) *hosted.Client { return client }
	remoteChannelOptions = []remotechan.Option{remotechan.WithHTTPClient(r.relay.Client())}
	t.Cleanup(func() { remoteHostedClient, remoteChannelOptions = prevClient, prevOpts })
	relayCA := r.relayCA()

	// A fake agent the remote's binding runs: it answers with the message it is given.
	bin := t.TempDir()
	writeExec(t, filepath.Join(bin, "fake-agent"), "#!/bin/sh\ncat\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	r.serveRelease(pinnedVersion)

	if out, err := runRoot(t, "remote", "create", "edge"); err != nil {
		t.Fatalf("remote create: %v\n%s", err, out)
	}
	dir := filepath.Join(r.repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge")
	toml := fmt.Sprintf(`satelle_version = %q
secrets = []
wake_key = "REMOTE_WAKE_KEY"
lifecycle = %q

[[repo]]
url = %q
ref = "main"

[network]
allow = ["extra.example.net"]

[identity]
label = "edge-1"

[host]
start = ["sh", "-c", 'echo "start $0" >> %s', "{label}"]
network = ["sh", "-c", 'echo "network $0 $1" >> %s', "{label}", "{allow}"]
run = ["sh", "-c", "{command}"]
stop = ["sh", "-c", 'echo "stop $0" >> %s', "{label}"]
bootstrap = [["sh", "-c", 'echo bootstrapped > "$HOME/bootstrap-ran"']]

[host.env]
HOME = %q
SATELLE_CONFIG = ""
SATELLE_HOME = %q
SATELLE_TEST_VERSION = %q
SSL_CERT_FILE = %q
GIT_SSL_CAINFO = %q

[[segment]]
entry = "in_progress"
exit = "done"

[[trigger]]
name = "manual"
kind = "manual"
`, pinnedVersion, lifecycle, r.git.URL, r.hostLog, r.hostLog, r.hostLog, r.nodeHome, r.nodeHome, pinnedVersion, relayCA, r.git.CAFile)
	if err := os.WriteFile(filepath.Join(dir, "remote.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	agents := fmt.Sprintf("[worker]\nrole = \"agent\"\ncommand = %q\ntools = \"read_file\"\n", workerCommand)
	if err := os.WriteFile(filepath.Join(dir, "workflows", "agents.toml"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "remote", "validate", "edge"); err != nil {
		t.Fatalf("the fixture remote must validate: %v\n%s", err, out)
	}
	return r
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// relayCA writes the relay's TLS certificate as PEM, for the node's process to trust.
func (r *remoteRig) relayCA() string {
	r.t.Helper()
	u, _ := url.Parse(r.server)
	conn, err := tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		r.t.Fatal(err)
	}
	defer conn.Close()
	path := filepath.Join(r.t.TempDir(), "relay-ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: conn.ConnectionState().PeerCertificates[0].Raw})
	if err := os.WriteFile(path, pemBytes, 0o644); err != nil {
		r.t.Fatal(err)
	}
	return path
}

// serveRelease publishes version the way release.yml names it — the installer
// beside the asset, and <asset>.sha256 — where the asset is a wrapper that runs
// this test binary as the satelle CLI.
func (r *remoteRig) serveRelease(version string) {
	t := r.t
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := []byte("#!/bin/sh\nSATELLE_CLI_REEXEC=1 exec '" + exe + "' \"$@\"\n")
	sum := sha256.Sum256(wrapper)
	asset := fmt.Sprintf("satelle-v%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	installer, err := os.ReadFile(r.installer)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v" + version + "/install.sh":
			_, _ = w.Write(installer)
		case "/v" + version + "/" + asset:
			_, _ = w.Write(wrapper)
		case "/v" + version + "/" + asset + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SATELLE_RELEASE_BASE", srv.URL)
}

func (r *remoteRig) log() string {
	b, _ := os.ReadFile(r.hostLog)
	return string(b)
}

// ledgerKinds counts the repo's ledger entries of kind.
func (r *remoteRig) ledgerKinds(kind string) int {
	r.t.Helper()
	a, err := app.Open()
	if err != nil {
		r.t.Fatal(err)
	}
	defer a.Close()
	rows, err := a.Store.Ledger.List(context.Background(), ledger.ListFilter{Kind: kind})
	if err != nil {
		r.t.Fatal(err)
	}
	return len(rows)
}

// nodeSatelle runs the satelle CLI in the node's root, as the node would.
func (r *remoteRig) nodeSatelle(args ...string) string {
	r.t.Helper()
	exe, _ := os.Executable()
	cmd := exec.Command(exe, args...)
	cmd.Dir = r.nodeRoot
	cmd.Env = append(os.Environ(), "SATELLE_CLI_REEXEC=1", "HOME="+r.nodeHome, "SATELLE_HOME="+r.nodeHome, "SATELLE_CONFIG=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("satelle %v in the node: %v\n%s", args, err, out)
	}
	return string(out)
}

// AC4/AC6/AC7: `remote start` starts a fresh node from the local definition alone.
// The pinned version is installed from the release server, the definition's own
// bootstrap runs, `remote join` creates the data dir and store in an empty node
// root, unpacks and reindexes the substrate, fetches the repo, and the node
// reports ready; its agent answers a message; the run ends under the lifecycle.
func TestRemoteStartEndToEnd(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	if _, err := os.Stat(filepath.Join(r.nodeRoot, ".satelle")); err == nil {
		t.Fatal("the node root must start with no .satelle")
	}

	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_start", "--message", "hello node")
	if err != nil {
		t.Fatalf("remote start: %v\n%s", err, out)
	}

	for _, want := range []string{
		"remote edge: ready",
		"satelle    " + pinnedVersion,
		"workflows  *",
		"bindings   worker",
		"repo       tool @ main (" + r.git.Head[:12] + ")",
		"hello node", // the agent's reply: the fake agent echoes its message
		"remote edge: stopped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	allow := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  allow ") {
			allow = line
		}
	}
	for _, h := range append(release.Hosts(), "extra.example.net", "127.0.0.1", "*.127.0.0.1") {
		if !strings.Contains(allow, h) {
			t.Errorf("the reported allowlist lacks %s: %q", h, allow)
		}
	}

	// The fake host saw the host commands in order, with {allow} filled.
	log := r.log()
	iStart, iNet, iStop := strings.Index(log, "start edge-1"), strings.Index(log, "network edge-1 "), strings.Index(log, "stop edge-1")
	if iStart < 0 || iNet < iStart || iStop < iNet {
		t.Errorf("host commands out of order or missing:\n%s", log)
	}
	if !strings.Contains(log, "extra.example.net") || !strings.Contains(log, "github.com") {
		t.Errorf("host.network was not handed the allowlist:\n%s", log)
	}

	// On the node: init made the data dir and store, the remote's own substrate
	// is in place, the definition's bootstrap ran, and the repo is fetched.
	for _, rel := range []string{".satelle/satelle.toml", ".satelle/workflows/done.toml", ".satelle/workflows/agents.toml", ".satelle/skills/remote-work.md", "repos/tool/README"} {
		if _, err := os.Stat(filepath.Join(r.nodeRoot, filepath.FromSlash(rel))); err != nil {
			t.Errorf("the node lacks %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.nodeHome, "bootstrap-ran")); err != nil {
		t.Error("host.bootstrap did not run on the node")
	}
	remoteDone, _ := os.ReadFile(filepath.Join(r.repo, ".satelle", "config", "remotes", "edge", "workflows", "done.toml"))
	nodeDone, _ := os.ReadFile(filepath.Join(r.nodeRoot, ".satelle", "workflows", "done.toml"))
	if string(remoteDone) != string(nodeDone) {
		t.Error("the node's done.toml is not the remote's")
	}
	// Reindexed: the node's index lists the remote's skill, which init never wrote.
	if docs := r.nodeSatelle("doc", "list", "--kind", "skills", "--json"); !strings.Contains(docs, "remote-work") {
		t.Errorf("the node's index lacks the remote's skill; it was not reindexed:\n%s", docs)
	}
	// Nothing secret was written to the node: no token, no .env, no credential.
	if _, err := os.Stat(filepath.Join(r.nodeRoot, ".satelle", "config", ".env")); err == nil {
		t.Error("a .env was written to the node")
	}

	if got := r.ledgerKinds(ledger.KindRemoteStart); got != 1 {
		t.Errorf("remote_start entries = %d, want 1", got)
	}
	if got := r.ledgerKinds(ledger.KindRemoteStop); got != 1 {
		t.Errorf("remote_stop entries = %d, want 1", got)
	}
}

// declarePreflight appends a step with one probe per entry of probes (argv
// joined by spaces, expecting expect) to the rig's definition. The rig's host.run
// really runs the command, so a probe is a real command on this machine standing
// in for the host.
func (r *remoteRig) declarePreflight(expect string, probes ...string) {
	r.t.Helper()
	expects := make([]string, len(probes))
	for i := range probes {
		expects[i] = expect
	}
	r.declareProbes(expects, probes)
}

// declareProbes declares the probes under one step, each with its own
// expectation: expects[i] is the expectation of the shell command probes[i].
func (r *remoteRig) declareProbes(expects, probes []string) {
	r.t.Helper()
	path := filepath.Join(r.repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	var sb strings.Builder
	sb.WriteString("\n[[step]]\nname = \"in_progress\"\n")
	for i, p := range probes {
		fmt.Fprintf(&sb, "[[step.probe]]\nargv = [\"sh\", \"-c\", %q]\nexpect = %q\n", p, expects[i])
	}
	if err := os.WriteFile(path, append(b, sb.String()...), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if out, err := runRoot(r.t, "remote", "validate", "edge"); err != nil {
		r.t.Fatalf("the preflight fixture must validate: %v\n%s", err, out)
	}
}

// preflightRows lists the story's remote_preflight ledger rows.
func (r *remoteRig) preflightRows(storyID string) []ledger.Entry {
	r.t.Helper()
	a, err := app.Open()
	if err != nil {
		r.t.Fatal(err)
	}
	defer a.Close()
	rows, err := a.Store.Ledger.List(context.Background(), ledger.ListFilter{Kind: ledger.KindRemotePreflight, StoryID: storyID})
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// A real start prints every preflight check and records each as a
// remote_preflight row on the --story story, before the node reports ready.
func TestRemoteStartPrintsAndRecordsThePreflight(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	r.declareProbes([]string{"succeed", "succeed", "refused"}, []string{"true", "echo hello", "exit 3"})

	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_pf")
	if err != nil {
		t.Fatalf("remote start: %v\n%s", err, out)
	}
	// A passing line states the expectation beside the result, for a probe
	// expected to succeed and for one expected to be refused.
	for _, want := range []string{
		"preflight ok",
		"in_progress probe sh -c true: expected succeed, got exit 0 (pass)",
		"in_progress probe sh -c echo hello: expected succeed, got exit 0 (pass)",
		"in_progress probe sh -c exit 3: expected refused, got exit 3 (pass)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "preflight ok") > strings.Index(out, "remote edge: ready") {
		t.Errorf("the preflight must be reported before the node is ready:\n%s", out)
	}
	rows := r.preflightRows("sty_pf")
	if len(rows) != 3 {
		t.Fatalf("remote_preflight rows for the story = %d, want 3", len(rows))
	}
	for _, row := range rows {
		var p struct {
			Remote, Step, Kind, Expect, Outcome string
			Pass                                bool
			Argv                                []string
		}
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Remote != "edge" || p.Step != "in_progress" || p.Kind != "probe" || (p.Expect != "succeed" && p.Expect != "refused") || p.Outcome != "pass" || !p.Pass || len(p.Argv) != 3 {
			t.Errorf("row payload = %s", row.Payload)
		}
	}
	if n := len(r.preflightRows("sty_start")); n != 0 {
		t.Errorf("rows leaked onto another story: %d", n)
	}
}

// A probe whose outcome differs from its expectation stops the start before the
// node is bootstrapped; host.stop runs and the error names the step and probe.
func TestRemoteStartStopsOnAFailedProbe(t *testing.T) {
	r := newRemoteRig(t, "idle", "fake-agent --system {system} --tools {tools}")
	r.declarePreflight("refused", "true") // exits 0 where a refusal was expected

	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_pf")
	if err == nil {
		t.Fatalf("start must fail:\n%s", out)
	}
	for _, want := range []string{"in_progress", "sh -c true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
	if !strings.Contains(out, "preflight FAIL") || strings.Contains(out, "remote edge: ready") {
		t.Errorf("want the failed check printed and no ready report:\n%s", out)
	}
	log := r.log()
	if !strings.Contains(log, "stop edge-1") {
		t.Errorf("host.stop must run after a failed preflight, even for an idle remote:\n%s", log)
	}
	if _, serr := os.Stat(filepath.Join(r.nodeHome, "bootstrap-ran")); serr == nil {
		t.Error("the node was bootstrapped despite the failed preflight")
	}
	if rows := r.preflightRows("sty_pf"); len(rows) != 1 {
		t.Errorf("remote_preflight rows = %d, want 1", len(rows))
	}
}

// --story is required: a result with no story to carry it is refused.
func TestRemoteStartRequiresAStory(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	if _, err := runRoot(t, "remote", "start", "edge", "--server", r.server); err == nil {
		t.Error("remote start without --story must be refused")
	}
	if r.log() != "" {
		t.Errorf("no host command may run without --story:\n%s", r.log())
	}
}

// AC9: an idle remote is left running: no host.stop, and the run says so.
func TestRemoteStartIdleLeavesTheNodeRunning(t *testing.T) {
	r := newRemoteRig(t, "idle", "fake-agent --system {system} --tools {tools}")
	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_start")
	if err != nil {
		t.Fatalf("remote start: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remote edge: left idle") || strings.Contains(out, "stopped") {
		t.Errorf("want the run to say it left the node idle:\n%s", out)
	}
	if strings.Contains(r.log(), "stop edge-1") {
		t.Errorf("host.stop must not run for an idle remote:\n%s", r.log())
	}
	if r.ledgerKinds(ledger.KindRemoteIdle) != 1 || r.ledgerKinds(ledger.KindRemoteStop) != 0 {
		t.Error("an idle run records remote_idle and no remote_stop")
	}
}

// AC6: a binding that does not resolve on the node fails the start, naming it.
func TestRemoteStartUnresolvedBindingFailsNamingIt(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "no-such-agent-binary --system {system}")
	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_start")
	if err == nil {
		t.Fatalf("start must fail:\n%s", out)
	}
	msg := err.Error()
	for _, want := range []string{`binding "worker"`, "no-such-agent-binary"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should name %q", msg, want)
		}
	}
	if strings.Contains(out, "remote edge: ready") {
		t.Errorf("a node with an unresolved binding must not be reported ready:\n%s", out)
	}
	if !strings.Contains(r.log(), "stop edge-1") {
		t.Errorf("a failed start still ends the run under its lifecycle:\n%s", r.log())
	}
}

// AC6: a node running another satelle than the definition pins fails the start,
// naming both versions.
func TestRemoteStartWrongVersionFailsNamingIt(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	// The installed "release" is built to report a different version than pinned.
	path := filepath.Join(r.repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "SATELLE_TEST_VERSION = \""+pinnedVersion+"\"", `SATELLE_TEST_VERSION = "1.2.4"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_start")
	if err == nil {
		t.Fatalf("start must fail:\n%s", out)
	}
	for _, want := range []string{"1.2.4", pinnedVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

// An invalid definition is refused before any host command runs.
func TestRemoteStartRefusesAnInvalidDefinition(t *testing.T) {
	r := newRemoteRig(t, "shutdown", "fake-agent --system {system} --tools {tools}")
	path := filepath.Join(r.repo, ".satelle", filepath.FromSlash(config.RemotesDir), "edge", "remote.toml")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), `lifecycle = "shutdown"`, `lifecycle = "forever"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "remote", "start", "edge", "--server", r.server, "--story", "sty_start")
	if err == nil || !strings.Contains(out, "remote.toml: lifecycle") {
		t.Fatalf("want a validation failure naming remote.toml: lifecycle; err=%v\n%s", err, out)
	}
	if r.log() != "" {
		t.Errorf("no host command may run for an invalid definition:\n%s", r.log())
	}
	if _, err := runRoot(t, "remote", "start", "ghost", "--server", r.server, "--story", "sty_start"); err == nil {
		t.Error("an undefined remote must be refused")
	}
}

// AC7: a SATELLE_RELEASE_BASE forwarded to the node puts the mirror's host in
// the allowlist; with none set the hosts are exactly the release hosts.
func TestReleaseHostsIncludesTheMirror(t *testing.T) {
	if got := releaseHosts(""); len(got) != len(release.Hosts()) {
		t.Errorf("no base: %v, want only the release hosts", got)
	}
	got := releaseHosts("https://mirror.example.net:8443/releases")
	if last := got[len(got)-1]; last != "mirror.example.net" {
		t.Errorf("the mirror host is missing from %v", got)
	}
}
