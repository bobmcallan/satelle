package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/snapsync"
)

// Two-machine tests for `sync --force` (sty_796fc16d), on the harness of
// sync_snapshot_test.go.

// hostedRecord is the snapshot record of an area at a pinned version (0 = head).
func hostedRecord(t *testing.T, f *snapFake, area string, version int) snapsync.Record {
	t.Helper()
	raw, _, ok := f.cfg.get("probe", snapsyncRecord(area), version)
	if !ok {
		t.Fatalf("no %s record at version %d on the hosted copy", area, version)
	}
	r, err := snapsync.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// runCLI runs a real command line as this machine, against the fake server.
func (m *snapMachine) runCLI(args ...string) (string, error) {
	m.t.Helper()
	m.use()
	return runRoot(m.t, append(args, "--server", m.url)...)
}

// AC1/AC2: B is behind after A's later pushes. A plain push is refused and
// uploads nothing; `sync config push --force` publishes B's tree as the next
// snapshot on the hosted parent, drops the file only A had, and a later deploy
// sees B's tree.
func TestSnapshotForcePushOverridesNewerHosted(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b := newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}

	a.write("skills/a.md", "a0\n")
	a.write("skills/b.md", "b0\n")
	a.pushConfig() // snapshot 1
	b.deploy()
	a.write("skills/a.md", "a1\n")
	a.pushConfig() // snapshot 2
	a.write("skills/aonly.md", "only A\n")
	a.pushConfig() // snapshot 3
	b.write("skills/b.md", "b1\n")

	// Without --force: refused, nothing uploaded.
	before := len(f.putLog())
	if out, err := b.tryPushConfig(); err == nil || !strings.Contains(err.Error(), "snapshot 3") {
		t.Fatalf("a behind push without --force: err=%v\n%s", err, out)
	}
	if out, err := b.runCLI("sync", "config", "push"); err == nil {
		t.Fatalf("the CLI pushed over a newer snapshot without --force:\n%s", out)
	}
	if n := len(f.putLog()) - before; n != 0 {
		t.Errorf("a refused push made %d PUT(s): %v", n, f.putLog()[before:])
	}

	out, err := b.runCLI("sync", "config", "push", "--force")
	if err != nil {
		t.Fatalf("sync config push --force: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skills: snapshot 4 (parent 3, forced over hosted 3; this machine had 1)") {
		t.Errorf("the override is not named:\n%s", out)
	}
	rec := hostedRecord(t, f, "skills", 0)
	if rec.Parent != 3 {
		t.Errorf("forced snapshot parent = %d, want the hosted snapshot 3", rec.Parent)
	}
	if _, ok := rec.Files["skills/aonly.md"]; ok {
		t.Errorf("a file only A had must be absent from the forced snapshot: %v", rec.Files)
	}
	if rec.Files["skills/b.md"] != sha256hex([]byte("b1\n")) || rec.Files["skills/a.md"] != sha256hex([]byte("a0\n")) {
		t.Errorf("the forced snapshot is not B's tree: %v", rec.Files)
	}
	if got, _, _ := f.cfg.get("probe", "skills/b.md", 0); string(got) != "b1\n" {
		t.Errorf("B's bytes did not land: %q", got)
	}
	if st, ok := b.base("skills"); !ok || st.Version != 4 {
		t.Errorf("B's base after the forced push = %+v ok=%v", st, ok)
	}

	// A later deploy sees B's tree.
	c := newSnapMachine(t, ts.URL)
	c.deploy()
	if c.read("skills/a.md") != "a0\n" || c.read("skills/b.md") != "b1\n" || c.has("skills/aonly.md") {
		t.Errorf("a fresh deploy is not B's tree: a=%q b=%q aonly=%v", c.read("skills/a.md"), c.read("skills/b.md"), c.has("skills/aonly.md"))
	}
	// A is now the machine that is behind, and fast-forwards to B's tree.
	if out, err := a.tryPushConfig(); err == nil {
		t.Errorf("A pushed over the forced snapshot without pulling:\n%s", out)
	}
	a.deploy()
	if a.read("skills/a.md") != "a0\n" || a.has("skills/aonly.md") {
		t.Errorf("A did not fast-forward to B's tree: a=%q aonly=%v", a.read("skills/a.md"), a.has("skills/aonly.md"))
	}
}

// AC1: the same override for the documents push and for bare `satelle sync
// --force`, which applies it to the config and documents pushes.
func TestSnapshotForceDocumentsAndBareSync(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b := newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}

	a.write("documents/d.md", "d0\n")
	a.write("skills/s.md", "s0\n")
	a.pushDocs() // documents snapshot 1
	a.pushConfig()
	b.pullDocs()
	b.deploy()
	a.write("documents/d.md", "d1\n")
	a.write("skills/s.md", "s1\n")
	a.pushDocs() // documents snapshot 2
	a.pushConfig()
	a.write("documents/aonly.md", "only A\n")
	a.write("skills/aonly.md", "only A\n")
	a.pushDocs() // documents snapshot 3
	a.pushConfig()
	b.write("documents/d.md", "d2\n")
	b.write("skills/s.md", "s2\n")

	if out, err := b.runCLI("sync", "documents", "push"); err == nil {
		t.Fatalf("documents push over a newer snapshot without --force:\n%s", out)
	}
	out, err := b.runCLI("sync", "documents", "push", "--force")
	if err != nil {
		t.Fatalf("sync documents push --force: %v\n%s", err, out)
	}
	if !strings.Contains(out, "documents: snapshot 4 (parent 3, forced over hosted 3; this machine had 1)") {
		t.Errorf("the documents override is not named:\n%s", out)
	}
	rec := hostedRecord(t, f, "documents", 0)
	if _, ok := rec.Files["documents/aonly.md"]; ok || rec.Parent != 3 || rec.Files["documents/d.md"] != sha256hex([]byte("d2\n")) {
		t.Errorf("forced documents snapshot = parent %d files %v", rec.Parent, rec.Files)
	}

	// Bare sync --force publishes the config tree the same way; its workstate push
	// never takes --force.
	out, err = b.runCLI("sync", "--force")
	if err != nil {
		t.Fatalf("sync --force: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skills: snapshot 4 (parent 3, forced over hosted 3; this machine had 1)") {
		t.Errorf("bare sync --force did not force the config push:\n%s", out)
	}
	rec = hostedRecord(t, f, "skills", 0)
	if _, ok := rec.Files["skills/aonly.md"]; ok || rec.Files["skills/s.md"] != sha256hex([]byte("s2\n")) {
		t.Errorf("bare sync --force skills snapshot = %v", rec.Files)
	}
}

// AC3: --force overrides the hosted snapshot, never a local conflict copy: the
// push is refused and no snapshot is written.
func TestSnapshotForceStillRefusedWhileUnmerged(t *testing.T) {
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	a, b := newSnapMachine(t, ts.URL), newSnapMachine(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}

	a.write("skills/x.md", "base\n")
	a.pushConfig()
	b.deploy()
	a.write("skills/x.md", "from A\n")
	a.pushConfig() // snapshot 2
	b.write("skills/x.md", "from B\n")
	b.deploy() // conflict: A's bytes parked, B's kept

	before := len(f.putLog())
	out, err := b.runCLI("sync", "config", "push", "--force")
	if err == nil || !strings.Contains(err.Error(), "unmerged skills/x.md") {
		t.Fatalf("--force over an unresolved conflict: err=%v\n%s", err, out)
	}
	if n := len(f.putLog()) - before; n != 0 {
		t.Errorf("a refused forced push made %d PUT(s): %v", n, f.putLog()[before:])
	}
	if rec := hostedRecord(t, f, "skills", 0); rec.Parent != 1 {
		t.Errorf("a snapshot was written despite the conflict: parent %d", rec.Parent)
	}
}
