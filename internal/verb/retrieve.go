package verb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/bobmcallan/satelle/internal/retrieve"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{Name: "retrieve-get", Description: "Fetch the exact original bytes stored under a CCR content hash", Invoke: retrieveGetVerb})
}

// retrieveHashRE is the accepted key shape: 24 lowercase hex characters
// (retrieve.Hash's truncated sha256). Anything else is refused the same way an
// unknown hash is — the caller cannot tell "malformed" from "never stored"
// apart, and does not need to.
var retrieveHashRE = regexp.MustCompile(`^[0-9a-f]{24}$`)

type retrieveGetReq struct {
	Hash string `json:"hash"`
}

// retrieveGetResp base64-encodes the payload so the JSON verb envelope stays
// text-safe for arbitrary (including non-UTF-8) original bytes; the CLI
// verb decodes and writes the raw bytes to stdout itself.
type retrieveGetResp struct {
	DataBase64 string `json:"data_base64"`
}

func retrieveGetVerb(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireRetrieve()
	if err != nil {
		return nil, err
	}
	var req retrieveGetReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if !retrieveHashRE.MatchString(req.Hash) {
		return nil, fmt.Errorf("retrieve: no stored original for hash %s", req.Hash)
	}
	b, err := store.Get(ctx, req.Hash)
	if errors.Is(err, retrieve.ErrNotFound) {
		return nil, fmt.Errorf("retrieve: no stored original for hash %s", req.Hash)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(retrieveGetResp{DataBase64: base64.StdEncoding.EncodeToString(b)})
}

// retrieveKeepDays is the CCR retention horizon (satelle.toml
// retrieve_keep_days); 0 (default) disables pruning entirely — today's
// behaviour until an operator opts in.
var retrieveKeepDays int

// SetRetrieveRetention wires the CCR retention policy, resolved from
// satelle.toml at app init (sty_b0577532).
func SetRetrieveRetention(keepDays int) { retrieveKeepDays = keepDays }

// pruneRetrievalStore enforces CCR retention: a ref is expired once its owning
// story is terminal (the same terminalStoryStates rule storyretention.go
// applies to attachment dirs) and past retrieveKeepDays; a blob is deleted once
// no ref remains. No-op when retention is off or either store is unwired.
func pruneRetrievalStore(ctx context.Context, workStore *workitem.Store, now time.Time) (retrieve.PruneResult, error) {
	if retrieveKeepDays <= 0 || retrieveStore == nil || workStore == nil {
		return retrieve.PruneResult{}, nil
	}
	isTerminalOlderThan := func(storyID string, now time.Time, keepDays int) bool {
		item, err := workStore.Get(ctx, storyID)
		if err != nil {
			return false
		}
		if !terminalStoryStates[item.Status] {
			return false
		}
		return now.Sub(item.UpdatedAt) > time.Duration(keepDays)*24*time.Hour
	}
	return retrieveStore.Prune(ctx, now, retrieveKeepDays, isTerminalOlderThan)
}
