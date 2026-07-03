package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func init() {
	Register(&Verb{Name: "doc-list", Description: "List indexed authored docs (optionally by kind)", Invoke: docList})
	Register(&Verb{Name: "doc-get", Description: "Get one indexed authored doc by kind+name", Invoke: docGet})
	Register(&Verb{Name: "doc-sync", Description: "Run the directory monitor once over the authored dirs", Invoke: docSync})
}

// summaryBundleName is the story-implementation-summary OKF sub-bundle (kind
// documents). It is auto-generated commit evidence, not authored discovery
// substrate, so the default (lightweight) doc-list omits it (sty_adab13cb).
const summaryBundleName = "story-implementation-summary"

// docListReq filters the authored-doc index. Empty kind lists every kind.
// Full=true returns whole Doc records (bodies included) for consumers that need
// them (the web workflow renderer); the default is the lightweight index —
// kind/name/headline only, with the commit-summary bundle excluded — so the CLI
// discovery surface is a small fraction of the full-body dump (sty_adab13cb).
type docListReq struct {
	Kind string `json:"kind,omitempty"`
	Full bool   `json:"full,omitempty"`
}

// docListItem is the lightweight projection returned by the default doc-list:
// enough to discover a doc and address it with doc-get, without its body.
type docListItem struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Headline string `json:"headline,omitempty"`
}

func docList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireDocIndex()
	if err != nil {
		return nil, err
	}
	var req docListReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	docs, err := store.List(ctx, req.Kind)
	if err != nil {
		return nil, err
	}
	if req.Full {
		return json.Marshal(docs)
	}
	items := make([]docListItem, 0, len(docs))
	for _, d := range docs {
		if d.Kind == "documents" && d.Name == summaryBundleName {
			continue // auto-generated commit evidence, not discovery substrate
		}
		items = append(items, docListItem{Kind: d.Kind, Name: d.Name, Headline: d.Headline})
	}
	return json.Marshal(items)
}

// docGetReq addresses one indexed doc by (kind, name).
type docGetReq struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func docGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireDocIndex()
	if err != nil {
		return nil, err
	}
	var req docGetReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.Kind == "" || req.Name == "" {
		return nil, fmt.Errorf("verb: kind and name required")
	}
	doc, err := store.Get(ctx, req.Kind, req.Name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

// docSyncReq carries the kind→dir map to sync. The CLI supplies it from the
// resolved config (substrate roots); a web caller would do the same.
type docSyncReq struct {
	Dirs map[string]string `json:"dirs"`
}

func docSync(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireDocIndex()
	if err != nil {
		return nil, err
	}
	var req docSyncReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	res, err := store.Sync(ctx, req.Dirs, time.Now())
	if err != nil {
		return nil, err
	}
	if res.Indexed > 0 || res.Pruned > 0 {
		notifyChange(TopicDocs)
	}
	return json.Marshal(res)
}
