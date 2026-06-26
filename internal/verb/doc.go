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

// docListReq filters the authored-doc index. Empty kind lists every kind.
type docListReq struct {
	Kind string `json:"kind,omitempty"`
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
	return json.Marshal(docs)
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
	return json.Marshal(res)
}
