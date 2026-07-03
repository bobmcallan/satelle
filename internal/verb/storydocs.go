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
	file := safeName(req.Name)
	if file == "" {
		return nil, fmt.Errorf("verb: attach: a document name is required")
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "" {
		typ = "document"
	}
	dir := attachmentDir(item)
	if dir == "" {
		return nil, fmt.Errorf("verb: attach: attachment dir for %s (%s) not configured", req.StoryID, item.Kind)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("verb: attach: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nstory: %s\ntype: %s\nname: %s\n---\n\n%s\n",
		req.StoryID, typ, strings.TrimSuffix(file, ".md"), strings.TrimRight(req.Body, "\n"))
	if err := os.WriteFile(filepath.Join(dir, file), []byte(b.String()), 0o644); err != nil {
		return nil, fmt.Errorf("verb: attach: %w", err)
	}
	appendLedger(ctx, req.StoryID, KindStoryDocAttached,
		fmt.Sprintf("attached %s document %q", typ, strings.TrimSuffix(file, ".md")), time.Now())
	return json.Marshal(docRef{StoryID: req.StoryID, Name: strings.TrimSuffix(file, ".md"), Type: typ})
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
