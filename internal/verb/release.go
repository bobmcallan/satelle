package verb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-release-record",
		Description: "Record a release (commit + release id) against stories by id, done included; changes no status",
		Invoke:      storyReleaseRecord,
	})
}

// releaseFields is the repo's [release] fields: the extra metadata keys a
// release record may carry. Empty accepts any key.
var releaseFields []string

// SetReleaseFields wires the repo's [release] fields.
func SetReleaseFields(fields []string) { releaseFields = fields }

type releaseRecordReq struct {
	IDs     []string          `json:"ids"`
	Commit  string            `json:"commit"`
	Release string            `json:"release"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// releaseRecordPayload is the ledger payload for KindReleaseRecord.
type releaseRecordPayload struct {
	Commit  string            `json:"commit"`
	Release string            `json:"release"`
	Fields  map[string]string `json:"fields,omitempty"`
	Session string            `json:"session,omitempty"`
	Owner   string            `json:"owner,omitempty"`
}

type releaseRecordRow struct {
	StoryID string `json:"story_id"`
	Status  string `json:"status"`
	EntryID string `json:"entry_id"`
}

// storyReleaseRecord appends one release_record row to each named story. It is
// a record, not a transition: it takes no seat, writes no status and runs no
// definition check, so it works from any session and on a story already done.
// Every id and field key is judged before the first row is written, and the
// rows land in one transaction, so a refusal writes nothing.
func storyReleaseRecord(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req releaseRecordReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("verb: decode story-release-record: %w", err)
	}
	req.Commit, req.Release = strings.TrimSpace(req.Commit), strings.TrimSpace(req.Release)
	if req.Commit == "" || req.Release == "" {
		return nil, fmt.Errorf("verb: story-release-record: commit and release are both required")
	}
	if err := checkReleaseFields(req.Fields); err != nil {
		return nil, err
	}
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}

	var items []workitem.Item
	seen := map[string]bool{}
	for _, id := range req.IDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		it, gerr := store.Get(ctx, id)
		if errors.Is(gerr, workitem.ErrNotFound) {
			return nil, fmt.Errorf("verb: story-release-record: %s does not name a story; nothing was recorded", id)
		}
		if gerr != nil {
			return nil, fmt.Errorf("verb: story-release-record: read %s: %w", id, gerr)
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("verb: story-release-record: at least one story id is required")
	}

	payload, err := json.Marshal(releaseRecordPayload{
		Commit: req.Commit, Release: req.Release, Fields: req.Fields,
		Session: config.ResolveSession(), Owner: lease.ResolveOwner(),
	})
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("release %s @ %s", req.Release, req.Commit)
	now := time.Now().UTC()
	rows := make([]releaseRecordRow, 0, len(items))
	write := func(l *ledger.Store) error {
		rows = rows[:0]
		for _, it := range items {
			e, aerr := l.Append(ctx, ledger.AppendInput{
				StoryID: it.ID, Kind: ledger.KindReleaseRecord, Actor: "executor", Body: body, Payload: payload,
			}, now)
			if aerr != nil {
				return fmt.Errorf("verb: story-release-record: append on %s: %w", it.ID, aerr)
			}
			rows = append(rows, releaseRecordRow{StoryID: it.ID, Status: it.Status, EntryID: e.ID})
		}
		return nil
	}
	if txRunner != nil {
		err = txRunner(ctx, func(tx *sql.Tx) error { return write(ls.WithTx(tx)) })
	} else {
		err = write(ls)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(rows)
}

// checkReleaseFields refuses an empty key, and a key outside the declared
// [release] fields when that list is set.
func checkReleaseFields(fields map[string]string) error {
	declared := map[string]bool{}
	for _, f := range releaseFields {
		declared[strings.TrimSpace(f)] = true
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("verb: story-release-record: a release field needs a key")
		}
		if len(declared) > 0 && !declared[k] {
			return fmt.Errorf("verb: story-release-record: release field %q is not declared in [release] fields (%s); nothing was recorded",
				k, strings.Join(releaseFields, ", "))
		}
	}
	return nil
}
