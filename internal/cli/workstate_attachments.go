package cli

// Story attachments travel with the story (sty_4d42e13e). A story's markdown
// attachments — plan, step summaries, the type:change patch — live only in the
// machine's runtime stories dir, so without this leg another machine reads the
// story and its ledger but not what the story changed. Work-state sync carries
// them through the bound project's personal documents collection at
// stories/<id>/<file>.md, byte for byte.
//
// This REVERSES sty_948ad5df's decision that change patches stay off the wire.
// The reversal is deliberate (epic:substrate-continuity: the personal project is
// the substrate's only off-machine home) and bounded: nothing leaves the machine
// unless [sync] stories is personal or shared; binary attachments never leave it;
// and no attachment body ever rides the workstate items/ledger Apply — the
// documents route is the only way out.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// attachmentRoot is the documents-collection prefix story attachments live under.
// subsync.ExcludedLocal keeps it out of the config-document sync, so the two
// never see each other's files.
const attachmentRoot = "stories/"

func attachmentPath(id, file string) string { return attachmentRoot + id + "/" + file }

// attachmentKey is a path's key in the uploaded-state record: "<id>/<file>".
func attachmentKey(path string) string { return strings.TrimPrefix(path, attachmentRoot) }

// parseAttachmentPath splits stories/<id>/<file>.md; ok is false for any other
// document path, including one that would escape the attachment dir.
func parseAttachmentPath(p string) (id, file string, ok bool) {
	rest, found := strings.CutPrefix(p, attachmentRoot)
	if !found {
		return "", "", false
	}
	id, file, found = strings.Cut(rest, "/")
	if !found || !strings.HasPrefix(id, "sty_") || !strings.HasSuffix(file, ".md") ||
		strings.ContainsAny(file, "/\\") || file == ".." || id == ".." {
		return "", "", false
	}
	return id, file, true
}

func contentSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// isLocalStory reports whether id is a story row in this machine's store.
func isLocalStory(ctx context.Context, a *app.App, id string) bool {
	it, err := a.Store.Stories.Get(ctx, id)
	return err == nil && it.Kind == workitem.KindStory
}

// pendingAttachment is a local markdown attachment the hosted store has not
// confirmed at its current content.
type pendingAttachment struct {
	file    verb.AttachmentFile
	sha     string
	hostRel string
}

// pushStoryAttachments is the attachment leg of `sync workstate push`. It runs on
// every exit of the push that reached the server, including the ones that sent no
// rows, because which files to upload is decided by the per-file uploaded-state
// record alone, never by which item or ledger rows the push carried. A failed
// upload is reported and left out of the record, so the next push retries it.
// Nothing here can fail the push: its errors go to errw.
func pushStoryAttachments(ctx context.Context, out, errw io.Writer, a *app.App, optIn map[string]bool, clientFor func() *hosted.Client, server, project string, scope scopeSet) {
	if !optIn["stories"] {
		return
	}
	ids, err := verb.StoryAttachmentIDs()
	if err != nil {
		fmt.Fprintf(errw, "attachments: %v\n", err)
		return
	}
	uploaded, err := hosted.LoadUploadedAttachments(server, project, a.RepoRoot)
	if err != nil {
		fmt.Fprintf(errw, "attachments: load uploaded state: %v\n", err)
		return
	}
	pending := map[string][]pendingAttachment{}
	var unchanged, binary int
	for _, id := range ids {
		if !scope.HasItem(id) || !isLocalStory(ctx, a, id) {
			continue
		}
		files, nbin, ferr := verb.StoryAttachmentFiles(id)
		if ferr != nil {
			fmt.Fprintf(errw, "attachments: %s: %v\n", id, ferr)
			continue
		}
		binary += nbin
		for _, f := range files {
			sum := contentSHA(f.Content)
			rel := attachmentPath(id, f.Name)
			if uploaded[attachmentKey(rel)] == sum {
				unchanged++
				continue
			}
			pending[id] = append(pending[id], pendingAttachment{file: f, sha: sum, hostRel: rel})
		}
	}

	// A story whose code is not on the remote yet, or that another checkout holds,
	// is held for its attachments exactly as it is for its rows; its files stay out
	// of the record and go once the hold clears.
	var held int
	hold := func(id string) {
		held += len(pending[id])
		delete(pending, id)
	}
	if len(pending) > 0 {
		if on, herr := config.HoldUnpushed(a.Config); herr == nil && on {
			res, cerr := classifyHoldIDs(ctx, a, setOf(slices.Collect(maps.Keys(pending))))
			if cerr != nil {
				fmt.Fprintf(errw, "attachments: hold check: %v\n", cerr)
				return
			}
			for id := range res.held {
				hold(id)
			}
		}
	}
	var client *hosted.Client
	if len(pending) > 0 {
		client = clientFor()
		var probe []json.RawMessage
		for _, id := range slices.Sorted(maps.Keys(pending)) {
			probe = append(probe, mustJSON(map[string]string{"id": id}))
		}
		skipped, _, serr := partitionByHold(ctx, client, server, project, a.RepoRoot, probe)
		if serr != nil {
			fmt.Fprintf(errw, "attachments: hold check: %v\n", serr)
			return
		}
		for _, s := range skipped {
			hold(s.ItemID)
		}
	}

	var nUp, failed int
	confirmed := map[string]string{}
	stopped := false
	for _, id := range slices.Sorted(maps.Keys(pending)) {
		for _, p := range pending[id] {
			if stopped {
				failed++
				continue
			}
			if _, perr := client.PushDocumentFile(ctx, project, p.hostRel, p.file.Content); perr != nil {
				failed++
				fmt.Fprintf(errw, "attachment upload failed %s: %v\n", p.hostRel, perr)
				stopped = errors.Is(perr, hosted.ErrLoginRequired)
				continue
			}
			confirmed[attachmentKey(p.hostRel)] = p.sha
			nUp++
		}
	}
	if err := hosted.RecordUploadedAttachments(server, project, a.RepoRoot, confirmed); err != nil {
		fmt.Fprintf(errw, "attachments: record uploaded state: %v\n", err)
	}

	if nUp == 0 && failed == 0 && held == 0 && binary == 0 {
		return
	}
	line := fmt.Sprintf("attachments: %d uploaded, %d unchanged", nUp, unchanged)
	if held > 0 {
		line += fmt.Sprintf(", %d held with their stories", held)
	}
	if failed > 0 {
		line += fmt.Sprintf(", %d failed (retried next push)", failed)
	}
	if binary > 0 {
		line += fmt.Sprintf(", %d binary not mirrored", binary)
	}
	fmt.Fprintln(out, line)
}

func setOf(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// pullStoryAttachments is the attachment leg of `sync workstate pull` and
// `snapshot`: it lists the personal collection's changes since the stored cursor
// and writes each stories/<id>/<file>.md of a story this machine holds into the
// attachment store, so `satelle story doc` reads it. A local file that differs
// from hosted is kept and named unless force is set; a local file hosted lacks is
// never touched. Failures are reported and hold the cursor back so the next pull
// retries; they never fail the pull. A scoped pass (scope non-nil) lists
// everything, takes only the scoped stories and leaves the cursor alone.
func pullStoryAttachments(ctx context.Context, out, errw io.Writer, a *app.App, client *hosted.Client, server, project string, scope scopeSet, force bool) {
	cursor := ""
	if scope == nil && !force {
		c, err := hosted.LoadAttachmentCursor(server, project, a.RepoRoot)
		if err != nil {
			fmt.Fprintf(errw, "attachments: load cursor: %v\n", err)
			return
		}
		cursor = c
	}
	changes, err := client.ListDocumentChanges(ctx, project, cursor)
	if err != nil {
		fmt.Fprintf(errw, "attachments: list hosted documents: %v\n", err)
		return
	}

	local := map[string]map[string]string{} // story id → file → sha, read once per story
	localFiles := func(id string) map[string]string {
		if m, ok := local[id]; ok {
			return m
		}
		m := map[string]string{}
		files, _, _ := verb.StoryAttachmentFiles(id)
		for _, f := range files {
			m[f.Name] = contentSHA(f.Content)
		}
		local[id] = m
		return m
	}

	var written, same, failed int
	var kept []string
	complete := true
	confirmed := map[string]string{}
	for _, item := range changes.Items {
		id, file, ok := parseAttachmentPath(item.Path)
		if !ok || !scope.HasItem(id) {
			continue
		}
		if !isLocalStory(ctx, a, id) {
			// The story may arrive on a later pass; do not move the cursor past its files.
			complete = false
			continue
		}
		rel := attachmentKey(item.Path)
		localSHA, have := localFiles(id)[file]
		if have && strings.EqualFold(localSHA, item.BlobSHA256) {
			same++
			confirmed[rel] = localSHA
			continue
		}
		body, _, ferr := client.DocumentFileContent(ctx, project, item.Path)
		if ferr != nil {
			if errors.Is(ferr, hosted.ErrDocumentFileMissing) {
				continue // listed then deleted
			}
			fmt.Fprintf(errw, "attachment fetch failed %s: %v\n", item.Path, ferr)
			failed++
			complete = false
			if errors.Is(ferr, hosted.ErrLoginRequired) {
				break
			}
			continue
		}
		hostedSHA := contentSHA(body)
		switch {
		case have && localSHA == hostedSHA:
			same++
			confirmed[rel] = hostedSHA
		case have && !force:
			kept = append(kept, item.Path)
		default:
			if werr := verb.WriteStoryAttachmentFile(id, file, body); werr != nil {
				fmt.Fprintf(errw, "attachment write failed %s: %v\n", item.Path, werr)
				failed++
				complete = false
				continue
			}
			localFiles(id)[file] = hostedSHA
			confirmed[rel] = hostedSHA
			written++
		}
	}
	// What this machine now holds identical to hosted need not be uploaded back.
	if err := hosted.RecordUploadedAttachments(server, project, a.RepoRoot, confirmed); err != nil {
		fmt.Fprintf(errw, "attachments: record uploaded state: %v\n", err)
	}
	if scope == nil && complete && changes.Cursor != "" {
		if err := hosted.SaveAttachmentCursor(server, project, a.RepoRoot, changes.Cursor); err != nil {
			fmt.Fprintf(errw, "attachments: save cursor: %v\n", err)
		}
	}
	for _, p := range kept {
		fmt.Fprintf(out, "kept local %s — differs from hosted; --force takes hosted\n", p)
	}
	if written+same+len(kept)+failed == 0 {
		return
	}
	fmt.Fprintf(out, "attachments: %d written, %d unchanged, %d kept (local differs; --force to take hosted)", written, same, len(kept))
	if failed > 0 {
		fmt.Fprintf(out, ", %d failed (retried next pull)", failed)
	}
	fmt.Fprintln(out)
}
