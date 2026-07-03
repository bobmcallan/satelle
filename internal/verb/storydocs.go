package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{Name: "story-doc-attach", Description: "Attach a typed markdown document to a story", Invoke: storyDocAttach})
	Register(&Verb{Name: "story-doc-list", Description: "List a story's attached documents", Invoke: storyDocList})
	Register(&Verb{Name: "story-doc-get", Description: "Read one of a story's attached documents", Invoke: storyDocGet})
}

// KindStoryDocAttached records a document attachment on the story's ledger.
const KindStoryDocAttached = "story_doc_attached"

// attachmentDir resolves an item's attachment/bundle directory by KIND
// (sty_890b86cb). A story's docs live under .satelle/stories/<id>/, but a TASK or
// EXECUTION is NOT a story: its artifacts belong in the task's own bundle under
// .satelle/tasks/<tsk_id>/ (an execution roots at its PARENT task's folder). This
// closes the kind-blind root where `attach` on a task materialised under
// .satelle/stories/tsk_*/. Empty when the dir wiring for that kind is unset.
func attachmentDir(it workitem.Item) string {
	switch it.Kind {
	case workitem.KindTask:
		if taskDir == "" {
			return ""
		}
		return filepath.Join(taskDir, it.ID)
	case workitem.KindExecution:
		if taskDir == "" || it.ParentID == "" {
			return ""
		}
		return filepath.Join(taskDir, it.ParentID)
	default:
		if storyDir == "" {
			return ""
		}
		return filepath.Join(storyDir, it.ID)
	}
}

// resolveAttachmentDir looks up an item by id and returns its kind-resolved
// attachment dir, or an error if the item is unknown / its dir wiring is unset.
func resolveAttachmentDir(ctx context.Context, store *workitem.Store, id string) (string, error) {
	it, err := store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	dir := attachmentDir(it)
	if dir == "" {
		return "", fmt.Errorf("verb: attachment dir for %s (%s) not configured", id, it.Kind)
	}
	return dir, nil
}

// safeName reduces a doc name to a bare filename (no path traversal) and ensures
// a single .md extension.
func safeName(name string) string {
	n := filepath.Base(strings.TrimSpace(name))
	if n == "" || n == "." || n == ".." || n == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(n, ".md") + ".md"
}

type docAttachReq struct {
	StoryID string `json:"story_id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Body    string `json:"body"`
}

type docRef struct {
	StoryID string `json:"story_id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Body    string `json:"body,omitempty"`
}

func storyDocAttach(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req docAttachReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	item, err := store.Get(ctx, req.StoryID)
	if err != nil {
		return nil, fmt.Errorf("verb: attach: %s: %w", req.StoryID, err)
	}
	name, typ, err := writeAttachedDoc(ctx, item, req.Name, req.Type, req.Body, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(docRef{StoryID: req.StoryID, Name: name, Type: typ})
}

// writeAttachedDoc materialises a typed markdown document into an item's
// kind-resolved attachment dir (.satelle/stories/<id>/ or .satelle/tasks/<id>/)
// with the standard frontmatter, and records a KindStoryDocAttached ledger row. It
// is the shared core of the story-doc-attach verb and the step-summary deposit
// (sty_47d31300), so a dispatched agent can PULL prior step summaries via
// `story docs`/`story doc` — the same path it pulls the plan. Returns the bare
// (extension-less) doc name and the normalised type.
func writeAttachedDoc(ctx context.Context, item workitem.Item, name, typ, body string, now time.Time) (string, string, error) {
	file := safeName(name)
	if file == "" {
		return "", "", fmt.Errorf("verb: attach: a document name is required")
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		typ = "document"
	}
	dir := attachmentDir(item)
	if dir == "" {
		return "", "", fmt.Errorf("verb: attach: attachment dir for %s (%s) not configured", item.ID, item.Kind)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("verb: attach: %w", err)
	}
	bare := strings.TrimSuffix(file, ".md")
	var b strings.Builder
	fmt.Fprintf(&b, "---\nstory: %s\ntype: %s\nname: %s\n---\n\n%s\n",
		item.ID, typ, bare, strings.TrimRight(body, "\n"))
	if err := os.WriteFile(filepath.Join(dir, file), []byte(b.String()), 0o644); err != nil {
		return "", "", fmt.Errorf("verb: attach: %w", err)
	}
	appendLedger(ctx, item.ID, KindStoryDocAttached,
		fmt.Sprintf("attached %s document %q", typ, bare), now)
	return bare, typ, nil
}

func storyDocList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req docRef
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	dir, err := resolveAttachmentDir(ctx, store, req.StoryID)
	if err != nil {
		return nil, err
	}
	out := []docRef{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		typ, name := docMeta(string(data))
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, docRef{StoryID: req.StoryID, Name: name, Type: typ})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return json.Marshal(out)
}

func storyDocGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req docRef
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	dir, err := resolveAttachmentDir(ctx, store, req.StoryID)
	if err != nil {
		return nil, err
	}
	file := safeName(req.Name)
	if file == "" {
		return nil, fmt.Errorf("verb: doc: a document name is required")
	}
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil, fmt.Errorf("verb: doc %s/%s: %w", req.StoryID, req.Name, err)
	}
	typ, name := docMeta(string(data))
	if name == "" {
		name = strings.TrimSuffix(file, ".md")
	}
	return json.Marshal(docRef{StoryID: req.StoryID, Name: name, Type: typ, Body: string(data)})
}

// docMeta pulls the type/name from a doc file's frontmatter (best-effort). The
// returned values feed the doc list/get views.
func docMeta(s string) (typ, name string) {
	if !strings.HasPrefix(s, "---\n") {
		return "", ""
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "type":
			typ = strings.TrimSpace(v)
		case "name":
			name = strings.TrimSpace(v)
		}
	}
	return typ, name
}
