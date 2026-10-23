package cli

// Story attachments travel with the story (sty_4d42e13e): push uploads them to
// the personal documents collection, pull/snapshot write them back on another
// machine. Only the hosted server is faked; the CLI, stores and attachment dirs
// are real.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
)

const (
	storiesOnlyToml  = "[sync]\nstories = \"personal\"\nledger = \"local\"\n\n[hosted]\nproject = \"probe\"\n"
	attachmentPrefix = "/api/v1/projects/probe/documents/"
)

// fakeDocs is the hosted documents collection: path-keyed text, a changed-since
// list with cursors "c<N>", and injectable PUT failures.
type fakeDocs struct {
	mu      sync.Mutex
	files   map[string][]byte
	ver     map[string]int
	seq     int
	puts    []string // URL paths of PUTs, in order
	reqs    []string // URL paths of every documents request
	failPut map[string]int
	root    string // the documents route; the project's unless set (a team workspace's)
	refuse  int    // when non-zero, every PUT is answered with this status
}

func newFakeDocs() *fakeDocs {
	return &fakeDocs{files: map[string][]byte{}, ver: map[string]int{}, failPut: map[string]int{}}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (d *fakeDocs) routes(mux *http.ServeMux, _ *fakeWorkstateServer) {
	root := d.root
	if root == "" {
		root = "/api/v1/projects/{project}/documents"
	}
	mux.HandleFunc("PUT "+root+"/{path...}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.reqs = append(d.reqs, r.URL.Path)
		path := r.PathValue("path")
		if d.refuse != 0 {
			http.Error(w, "refused", d.refuse)
			return
		}
		if d.failPut[path] > 0 {
			d.failPut[path]--
			http.Error(w, "injected", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_, existed := d.files[path]
		d.files[path] = body
		d.seq++
		d.ver[path] = d.seq
		d.puts = append(d.puts, r.URL.Path)
		if existed {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"path": path, "version": d.seq, "blob_sha256": sha256Hex(body)})
	})
	mux.HandleFunc("GET "+root+"/{path...}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.reqs = append(d.reqs, r.URL.Path)
		body, ok := d.files[r.PathValue("path")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", sha256Hex(body))
		_, _ = w.Write(body)
	})
	mux.HandleFunc("GET "+root, func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.reqs = append(d.reqs, r.URL.Path)
		var since int
		if c := r.URL.Query().Get("since"); c != "" {
			_, _ = fmt.Sscanf(c, "c%d", &since)
		}
		items := []hosted.ConfigItem{}
		for _, p := range slices.Sorted(maps.Keys(d.files)) {
			if d.ver[p] > since {
				items = append(items, hosted.ConfigItem{Path: p, Version: d.ver[p], BlobSHA256: sha256Hex(d.files[p]), Size: int64(len(d.files[p]))})
			}
		}
		_ = json.NewEncoder(w).Encode(hosted.DocumentChanges{Items: items, Cursor: fmt.Sprintf("c%d", d.seq)})
	})
}

func (d *fakeDocs) putCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.puts)
}

func (d *fakeDocs) requestCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.reqs)
}

func (d *fakeDocs) file(path string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.files[path]
	return b, ok
}

// attachmentServer is the fake workstate server plus the documents collection.
func attachmentServer(t *testing.T) (url string, f *fakeWorkstateServer, d *fakeDocs) {
	t.Helper()
	d = newFakeDocs()
	ts, f := newFakeWorkstateServerWith(t, d.routes)
	seedCred(t, ts.URL)
	return ts.URL, f, d
}

// storyDocBody reads an attachment back through `satelle story doc`.
func storyDocBody(t *testing.T, id, name string) (string, error) {
	t.Helper()
	out, err := runRoot(t, "story", "doc", id, name)
	if err != nil {
		return "", err
	}
	var ref struct {
		Body string `json:"body"`
	}
	if derr := json.NewDecoder(strings.NewReader(out)).Decode(&ref); derr != nil {
		t.Fatalf("parse story doc: %v\n%s", derr, out)
	}
	return ref.Body, nil
}

func mustDocBody(t *testing.T, id, name string) string {
	t.Helper()
	body, err := storyDocBody(t, id, name)
	if err != nil {
		t.Fatalf("story doc %s %s: %v", id, name, err)
	}
	return body
}

func attachDoc(t *testing.T, id, name, typ, body string) {
	t.Helper()
	if out, err := runRoot(t, "story", "attach", id, "--name", name, "--type", typ, "--body", body); err != nil {
		t.Fatalf("attach %s: %v\n%s", name, err, out)
	}
}

func runPush(t *testing.T, url string, extra ...string) string {
	t.Helper()
	out, err := runRoot(t, append([]string{"sync", "workstate", "push", "--server", url}, extra...)...)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	return out
}

func syncWorkstateCLI(t *testing.T, url string, verbArgs ...string) string {
	t.Helper()
	args := append([]string{"sync", "workstate", verbArgs[0], "--server", url}, verbArgs[1:]...)
	out, err := runRoot(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", verbArgs, err, out)
	}
	return out
}

// TestAttachmentPushUploadsByteExact (AC1): every markdown attachment, the
// type:change patch included, reaches the personal documents collection, with the
// ledger area opted in and with it local. Unchanged files are not re-sent; an
// edited one is sent alone.
func TestAttachmentPushUploadsByteExact(t *testing.T) {
	for _, tc := range []struct{ name, toml string }{
		{"ledger personal", ledgerPushToml},
		{"ledger local", storiesOnlyToml},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, _, docs := attachmentServer(t)
			workstateRepo(t, tc.toml)
			id := createStoryID(t, "attachment push")
			attachDoc(t, id, "change-in_progress-done", "change", "diff --git a/x.go b/x.go\n+hello\n")
			attachDoc(t, id, "plan", "plan", "the plan")

			out := runPush(t, url)
			if !strings.Contains(out, "attachments: 2 uploaded") {
				t.Errorf("push should report the uploads: %q", out)
			}
			for _, name := range []string{"change-in_progress-done", "plan"} {
				got, ok := docs.file("stories/" + id + "/" + name + ".md")
				if !ok {
					t.Fatalf("%s not uploaded", name)
				}
				if want := mustDocBody(t, id, name); string(got) != want {
					t.Errorf("%s uploaded bytes differ:\n got %q\nwant %q", name, got, want)
				}
			}

			if docs.putCount() != 2 {
				t.Fatalf("PUTs = %d, want 2", docs.putCount())
			}
			runPush(t, url)
			if docs.putCount() != 2 {
				t.Errorf("unchanged re-push PUTs = %d, want no more", docs.putCount())
			}
			attachDoc(t, id, "plan", "plan", "the plan, revised")
			runPush(t, url)
			if docs.putCount() != 3 {
				t.Errorf("edited attachment PUTs = %d, want exactly one more", docs.putCount())
			}
			got, _ := docs.file("stories/" + id + "/plan.md")
			if want := mustDocBody(t, id, "plan"); string(got) != want {
				t.Errorf("revised plan bytes differ: got %q want %q", got, want)
			}
		})
	}
}

// TestAttachmentExposure (AC4, replaces sty_948ad5df's
// TestWorkstatePushCarriesNoAttachmentBodies): a planted change patch is never
// uploaded when stories are local; when opted in it goes only to the bound
// project's documents route; and it is never inside a workstate Apply.
func TestAttachmentExposure(t *testing.T) {
	const secret = "planted-change-attachment-secret-xyz-4d42e13e"

	t.Run("stories local: nothing uploaded", func(t *testing.T) {
		url, f, docs := attachmentServer(t)
		workstateRepo(t, "[sync]\nledger = \"personal\"\n\n[hosted]\nproject = \"probe\"\n")
		id := createStoryID(t, "exposure local")
		attachDoc(t, id, "change-in_progress-done", "change", "diff\n+"+secret+"\n")
		runPush(t, url)
		if n := docs.requestCount(); n != 0 {
			t.Errorf("documents requests with stories local = %d, want 0", n)
		}
		assertNoSecretInApply(t, f, secret)
	})

	t.Run("stories personal: documents route only", func(t *testing.T) {
		url, f, docs := attachmentServer(t)
		workstateRepo(t, ledgerPushToml)
		id := createStoryID(t, "exposure personal")
		attachDoc(t, id, "change-in_progress-done", "change", "diff\n+"+secret+"\n")
		runPush(t, url)
		if docs.putCount() != 1 {
			t.Fatalf("PUTs = %d, want 1", docs.putCount())
		}
		for _, p := range docs.puts {
			if !strings.HasPrefix(p, attachmentPrefix+"stories/"+id+"/") {
				t.Errorf("PUT went to %q, want under %sstories/%s/", p, attachmentPrefix, id)
			}
		}
		got, _ := docs.file("stories/" + id + "/change-in_progress-done.md")
		if !strings.Contains(string(got), secret) {
			t.Error("the patch should reach the documents collection")
		}
		assertNoSecretInApply(t, f, secret)
	})
}

func assertNoSecretInApply(t *testing.T, f *fakeWorkstateServer, secret string) {
	t.Helper()
	// Story events go by Chain/Append, not Apply (sty_9bdf08c4): the body is as
	// absent from them as from the rows.
	if f.chain.carries(secret) {
		t.Error("an attachment body must never appear in a story event")
	}
	appends := f.chain.appendCount()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts["probe"]) == 0 && appends == 0 {
		t.Fatal("expected at least one workstate Apply or story Append")
	}
	blob, _ := json.Marshal(f.posts)
	if strings.Contains(string(blob), secret) {
		t.Error("an attachment body must never appear in a workstate items/ledger POST")
	}
}

// TestAttachmentPushReportsBinary (AC4): binary attachments stay on the machine
// and are reported as not mirrored.
func TestAttachmentPushReportsBinary(t *testing.T) {
	url, _, docs := attachmentServer(t)
	workstateRepo(t, storiesOnlyToml)
	id := createStoryID(t, "binary report")
	attachDoc(t, id, "notes", "note", "text")
	binPath := filepath.Join(t.TempDir(), "shot.png")
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 0x49, 0x48, 0x44, 0x52, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}
	if err := os.WriteFile(binPath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "story", "attach", id, "--name", "shot.png", "--type", "screenshot",
		"--binary-file", binPath, "--content-type", "image/png"); err != nil {
		t.Fatalf("binary attach: %v\n%s", err, out)
	}
	out := runPush(t, url)
	if !strings.Contains(out, "1 binary not mirrored") {
		t.Errorf("push should report the binary attachment: %q", out)
	}
	if docs.putCount() != 1 {
		t.Errorf("PUTs = %d, want only the markdown note", docs.putCount())
	}
	for p := range docs.files {
		if strings.Contains(p, "shot.png") {
			t.Errorf("binary attachment uploaded as %q", p)
		}
	}
}

// TestAttachmentPushRetriesWithNoNewRows (AC5): a failed upload is reported, does
// not fail the push, and is sent by the next push even though that push has no
// new rows (the up-to-date exit).
func TestAttachmentPushRetriesWithNoNewRows(t *testing.T) {
	url, _, docs := attachmentServer(t)
	workstateRepo(t, storiesOnlyToml)
	id := createStoryID(t, "retry")
	attachDoc(t, id, "plan", "plan", "p")
	attachDoc(t, id, "summary", "step-summary", "s")
	docs.failPut["stories/"+id+"/plan.md"] = 1

	out := runPush(t, url)
	if !strings.Contains(out, "attachment upload failed stories/"+id+"/plan.md") {
		t.Errorf("a failed upload should be reported: %q", out)
	}
	if !strings.Contains(out, "1 uploaded") || !strings.Contains(out, "1 failed") {
		t.Errorf("summary should count one upload and one failure: %q", out)
	}
	if _, ok := docs.file("stories/" + id + "/plan.md"); ok {
		t.Fatal("the failed file must not be hosted yet")
	}

	out = runPush(t, url)
	if !strings.Contains(out, "up to date") {
		t.Fatalf("second push should have no new rows: %q", out)
	}
	if _, ok := docs.file("stories/" + id + "/plan.md"); !ok {
		t.Error("the retry on a push with no new rows should upload the failed file")
	}
	before := docs.putCount()
	runPush(t, url)
	if docs.putCount() != before {
		t.Errorf("a settled push PUTs = %d more, want none", docs.putCount()-before)
	}
}

// TestAttachmentBackfillAfterUpgrade (AC7): attachments behind the work-state
// cursor with no record of being uploaded go up on the next push, with no new
// rows and no --full.
func TestAttachmentBackfillAfterUpgrade(t *testing.T) {
	url, _, docs := attachmentServer(t)
	workstateRepo(t, storiesOnlyToml)
	id := createStoryID(t, "backfill")
	attachDoc(t, id, "change-in_progress-done", "change", "patch")
	attachDoc(t, id, "plan", "plan", "plan")
	runPush(t, url)
	if docs.putCount() != 2 {
		t.Fatalf("first push PUTs = %d, want 2", docs.putCount())
	}

	// The pre-upgrade state: the cursor has advanced, nothing records an upload.
	statePath := hosted.DocumentSyncStatePathOverride
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state["attachments_uploaded"]; !ok {
		t.Fatal("expected an uploaded-state record after the first push")
	}
	delete(state, "attachments_uploaded")
	raw, _ = json.Marshal(state)
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	out := runPush(t, url)
	if !strings.Contains(out, "up to date") {
		t.Fatalf("the backfill push should have no new rows: %q", out)
	}
	if docs.putCount() != 4 {
		t.Errorf("backfill PUTs = %d, want both attachments again (4 total)", docs.putCount())
	}
	runPush(t, url)
	if docs.putCount() != 4 {
		t.Errorf("a push after the backfill PUTs = %d total, want 4", docs.putCount())
	}
}

// TestAttachmentPushHoldsAndScope (AC5): a story held for unpushed code keeps its
// attachments back with it, a scoped push sends only the scoped story's, and the
// leg still runs when every row is held.
func TestAttachmentPushHoldsAndScope(t *testing.T) {
	t.Run("held stories keep their attachments back", func(t *testing.T) {
		docs := newFakeDocs()
		w := newHoldWorldWith(t, storiesOnlyToml, docs.routes)
		for _, id := range []string{w.dirty, w.unpushed, w.ok, w.plan} {
			attachDoc(t, id, "plan", "plan", "plan of "+id)
		}
		out := w.push(t)
		if !strings.Contains(out, "held with their stories") {
			t.Errorf("push should say attachments were held: %q", out)
		}
		for id, want := range map[string]bool{w.dirty: false, w.unpushed: false, w.ok: true, w.plan: true} {
			if _, got := docs.file("stories/" + id + "/plan.md"); got != want {
				t.Errorf("story %s attachment hosted = %v, want %v", id, got, want)
			}
		}
	})

	t.Run("a scoped push sends only its story", func(t *testing.T) {
		docs := newFakeDocs()
		w := newHoldWorldWith(t, storiesOnlyToml, docs.routes)
		attachDoc(t, w.ok, "plan", "plan", "ok plan")
		attachDoc(t, w.plan, "plan", "plan", "plan plan")
		w.push(t, w.ok)
		if _, ok := docs.file("stories/" + w.ok + "/plan.md"); !ok {
			t.Error("the scoped story's attachment should be uploaded")
		}
		if _, ok := docs.file("stories/" + w.plan + "/plan.md"); ok {
			t.Error("a story outside the scope must not be uploaded")
		}
	})

	t.Run("every row held still uploads the rest", func(t *testing.T) {
		docs := newFakeDocs()
		w := newHoldWorldWith(t, storiesOnlyToml, docs.routes)
		touch := func(priority string, ids ...string) {
			for _, id := range ids {
				if out, err := runRoot(t, "story", "set", id, "--priority", priority); err != nil {
					t.Fatalf("touch %s: %v\n%s", id, err, out)
				}
			}
		}
		// The held stories change after the others, so the first push can move the
		// items cursor past ok and plan and the second sees only held rows.
		touch("low", w.dirty, w.unpushed)
		w.push(t)
		attachDoc(t, w.ok, "late", "note", "attached after the cursor moved")
		touch("medium", w.dirty)
		out := w.push(t)
		if !strings.Contains(out, "after holding unpushed stories") {
			t.Fatalf("expected the everything-held exit: %q", out)
		}
		if _, ok := docs.file("stories/" + w.ok + "/late.md"); !ok {
			t.Error("the attachment leg should still run when every row is held")
		}
	})
}

// holdDocsServer is the fake workstate server with both the hosted hold surface
// and the documents collection.
func holdDocsServer(t *testing.T) (url string, hs *fakeHoldServer, docs *fakeDocs) {
	t.Helper()
	hs, docs = newFakeHoldServer(), newFakeDocs()
	ts, _ := newFakeWorkstateServerWith(t, func(mux *http.ServeMux, f *fakeWorkstateServer) {
		hs.routes(mux, f)
		docs.routes(mux, f)
	})
	seedCred(t, ts.URL)
	return ts.URL, hs, docs
}

// TestAttachmentLegRunsWhenTheRowPushFails (AC5): a push that fails — here the
// Apply answers 500 — still sends the attachments, and the push still reports its
// own error; the leg neither masks nor adds to it.
func TestAttachmentLegRunsWhenTheRowPushFails(t *testing.T) {
	url, f, docs := attachmentServer(t)
	workstateRepo(t, storiesOnlyToml)
	id := createStoryID(t, "apply fails")
	attachDoc(t, id, "plan", "plan", "survives a failed apply")
	f.mu.Lock()
	f.failOnPost = 1
	f.mu.Unlock()

	out, err := runRoot(t, "sync", "workstate", "push", "--server", url)
	if err == nil || !strings.Contains(err.Error(), "apply workstate") {
		t.Fatalf("the push should fail with its own apply error, got %v\n%s", err, out)
	}
	got, ok := docs.file("stories/" + id + "/plan.md")
	if !ok {
		t.Fatal("the attachment should be uploaded even though the row push failed")
	}
	if want := mustDocBody(t, id, "plan"); string(got) != want {
		t.Errorf("uploaded bytes differ: got %q want %q", got, want)
	}
}

// TestAttachmentLegRunsWhenEveryRowIsHeldElsewhere (AC5): the "nothing after hold
// partition" exit — every pending item belongs to a story another location holds —
// still uploads the rest, and the foreign-held story's attachment stays back.
func TestAttachmentLegRunsWhenEveryRowIsHeldElsewhere(t *testing.T) {
	url, hs, docs := holdDocsServer(t)
	workstateRepo(t, storiesOnlyToml)
	free := createStoryID(t, "free story")
	runPush(t, url) // moves the items cursor past the free story

	foreign := createStoryID(t, "held by another location")
	hs.setHolder(foreign, otherLoc, "desk")
	attachDoc(t, foreign, "plan", "plan", "stays with the holder")
	attachDoc(t, free, "late", "note", "attached after the cursor moved")

	out := runPush(t, url)
	if !strings.Contains(out, "No work-state rows to push after hold partition.") {
		t.Fatalf("expected the nothing-after-hold-partition exit: %q", out)
	}
	if _, ok := docs.file("stories/" + free + "/late.md"); !ok {
		t.Error("the attachment leg should still run on the hold-partition exit")
	}
	if _, ok := docs.file("stories/" + foreign + "/plan.md"); ok {
		t.Error("a story held by another location must keep its attachment back")
	}
	if !strings.Contains(out, "held with their stories") {
		t.Errorf("push should say the foreign-held attachment was held: %q", out)
	}
}

// TestAttachmentHeldElsewhereKeepsItsAttachmentBack (AC5): alongside a push that
// does send rows, a story another location holds sends neither its row nor its
// attachments; the free story's go.
func TestAttachmentHeldElsewhereKeepsItsAttachmentBack(t *testing.T) {
	url, hs, docs := holdDocsServer(t)
	repo := workstateRepo(t, storiesOnlyToml)
	free := createStoryID(t, "free story")
	foreign := createStoryID(t, "held by another location")
	hs.setHolder(foreign, otherLoc, "desk")
	attachDoc(t, free, "plan", "plan", "free plan")
	attachDoc(t, foreign, "plan", "plan", "foreign plan")

	out := runPush(t, url)
	if !strings.Contains(out, "skip "+foreign+" (held elsewhere)") {
		t.Errorf("the foreign-held row should be skipped: %q", out)
	}
	// Single-owner record (sty_52b93691 AC5): reported once with the takeover
	// pointer, and sent on the first push after the hold is released.
	if n := strings.Count(out, "skip "+foreign); n != 1 {
		t.Errorf("the foreign-held row is reported %d times, want once: %q", n, out)
	}
	if !strings.Contains(out, "satelle story hold takeover "+foreign) {
		t.Errorf("the skip line should point at takeover: %q", out)
	}
	if _, ok := docs.file("stories/" + free + "/plan.md"); !ok {
		t.Error("the free story's attachment should be uploaded")
	}
	if _, ok := docs.file("stories/" + foreign + "/plan.md"); ok {
		t.Error("the foreign-held story's attachment must stay back")
	}
	if !strings.Contains(out, "1 held with their stories") {
		t.Errorf("push should count the held attachment: %q", out)
	}

	// Once the hold is released the record goes on the first push after it.
	hs.mu.Lock()
	delete(hs.holders, foreign)
	hs.mu.Unlock()
	if err := hosted.ForgetHold(url, "probe", repo, foreign); err != nil {
		t.Fatal(err)
	}
	out = runPush(t, url)
	if strings.Contains(out, "skip "+foreign) {
		t.Errorf("a released story must not be skipped again: %q", out)
	}
	if !hs.f.hasItem("probe", foreign) {
		t.Error("the released story's record should be sent on the first push after release")
	}
}

// cliMachine is one checkout's identity for the CLI: its own home (so its own
// runtime and attachment dirs) and its own repo and config.
type cliMachine struct{ home, cfg string }

func newCLIMachine(t *testing.T, toml string) cliMachine {
	t.Helper()
	workstateRepo(t, toml)
	return cliMachine{home: os.Getenv("SATELLE_HOME"), cfg: os.Getenv("SATELLE_CONFIG")}
}

func (m cliMachine) use(t *testing.T) {
	t.Helper()
	t.Setenv("SATELLE_HOME", m.home)
	t.Setenv("SATELLE_CONFIG", m.cfg)
}

// TestAttachmentTwoMachinePullAndSnapshot (AC2): what machine A attached is read
// on machine B after the normal sync, through pull and through snapshot, and B
// does not upload back what it just received.
func TestAttachmentTwoMachinePullAndSnapshot(t *testing.T) {
	for _, verbName := range []string{"pull", "snapshot"} {
		t.Run(verbName, func(t *testing.T) {
			url, _, docs := attachmentServer(t)
			a := newCLIMachine(t, storiesOnlyToml)
			b := newCLIMachine(t, storiesOnlyToml)

			a.use(t)
			id := createStoryID(t, "two machines")
			attachDoc(t, id, "change-in_progress-done", "change", "diff --git a/f b/f\n+changed\n")
			attachDoc(t, id, "plan", "plan", "plan body")
			wantChange := mustDocBody(t, id, "change-in_progress-done")
			wantPlan := mustDocBody(t, id, "plan")
			runPush(t, url)

			b.use(t)
			if _, err := storyDocBody(t, id, "plan"); err == nil {
				t.Fatal("machine B should not have the attachment before syncing")
			}
			out := syncWorkstateCLI(t, url, verbName)
			if !strings.Contains(out, "attachments: 2 written") {
				t.Errorf("%s should report the attachments written: %q", verbName, out)
			}
			if got := mustDocBody(t, id, "change-in_progress-done"); got != wantChange {
				t.Errorf("change patch on B = %q, want %q", got, wantChange)
			}
			if got := mustDocBody(t, id, "plan"); got != wantPlan {
				t.Errorf("plan on B = %q, want %q", got, wantPlan)
			}

			before := docs.putCount()
			runPush(t, url)
			if docs.putCount() != before {
				t.Errorf("B uploaded %d file(s) it had just pulled", docs.putCount()-before)
			}

			// A attaches later, with no new rows: B still receives it.
			a.use(t)
			attachDoc(t, id, "late", "note", "attached later")
			runPush(t, url)
			b.use(t)
			syncWorkstateCLI(t, url, verbName)
			if got := mustDocBody(t, id, "late"); !strings.Contains(got, "attached later") {
				t.Errorf("late attachment on B = %q", got)
			}
		})
	}
}

// TestAttachmentPullNeverOverwritesSilently (AC3): a differing local attachment
// is kept and named without --force and replaced with it; a local attachment
// hosted lacks is always kept.
func TestAttachmentPullNeverOverwritesSilently(t *testing.T) {
	url, _, _ := attachmentServer(t)
	a := newCLIMachine(t, storiesOnlyToml)
	b := newCLIMachine(t, storiesOnlyToml)

	a.use(t)
	id := createStoryID(t, "conflict")
	runPush(t, url)
	b.use(t)
	syncWorkstateCLI(t, url, "pull") // the story arrives, with no attachments yet
	attachDoc(t, id, "plan", "plan", "machine B plan")
	attachDoc(t, id, "only-here", "note", "B only")
	wantB := mustDocBody(t, id, "plan")

	a.use(t)
	attachDoc(t, id, "plan", "plan", "machine A plan")
	wantA := mustDocBody(t, id, "plan")
	runPush(t, url)

	b.use(t)
	out := syncWorkstateCLI(t, url, "pull")
	if !strings.Contains(out, "kept local stories/"+id+"/plan.md") || !strings.Contains(out, "1 kept") {
		t.Errorf("a differing local file should be named as kept: %q", out)
	}
	if got := mustDocBody(t, id, "plan"); got != wantB {
		t.Errorf("without --force the local plan changed to %q, want %q", got, wantB)
	}

	out = syncWorkstateCLI(t, url, "pull", "--force")
	if got := mustDocBody(t, id, "plan"); got != wantA {
		t.Errorf("with --force hosted should win: plan = %q, want %q (%s)", got, wantA, out)
	}
	if got := mustDocBody(t, id, "only-here"); !strings.Contains(got, "B only") {
		t.Errorf("a local-only attachment must be kept, got %q", got)
	}
}

// TestAttachmentScopedPullLeavesTheCursor (AC2): a pull scoped to one story takes
// only that story's attachments and does not move the cursor past the others.
func TestAttachmentScopedPullLeavesTheCursor(t *testing.T) {
	url, _, _ := attachmentServer(t)
	a := newCLIMachine(t, storiesOnlyToml)
	b := newCLIMachine(t, storiesOnlyToml)

	a.use(t)
	x := createStoryID(t, "scope x")
	y := createStoryID(t, "scope y")
	runPush(t, url)
	b.use(t)
	syncWorkstateCLI(t, url, "pull")

	a.use(t)
	attachDoc(t, x, "plan", "plan", "x plan")
	attachDoc(t, y, "plan", "plan", "y plan")
	runPush(t, url)

	b.use(t)
	syncWorkstateCLI(t, url, "pull", x)
	if _, err := storyDocBody(t, x, "plan"); err != nil {
		t.Errorf("the scoped story's attachment should arrive: %v", err)
	}
	if _, err := storyDocBody(t, y, "plan"); err == nil {
		t.Error("a story outside the scope must not receive its attachment")
	}
	syncWorkstateCLI(t, url, "pull")
	if _, err := storyDocBody(t, y, "plan"); err != nil {
		t.Errorf("the scoped pull must not move the cursor past the other story: %v", err)
	}
}
