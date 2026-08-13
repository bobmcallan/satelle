package hosted

// Work-state wire shapes carried by gRPC Sync (Apply / Snapshot). The server
// stamps origin=cli-sync itself — the client never supplies it.

import "encoding/json"

// WorkstateIngest is a local→server batch of stories/executions (items) and
// ledger entries. Apply transports this over Sync; the JSON keys match the
// published api.WorkstateIngest shape.
type WorkstateIngest struct {
	Items  []json.RawMessage `json:"items"`
	Ledger []json.RawMessage `json:"ledger"`
}

// WorkstateIngestResult is the Apply response: counts of upserted rows.
type WorkstateIngestResult struct {
	Items  int `json:"items"`
	Ledger int `json:"ledger"`
}

// WorkstateItem is one mirrored work-state row from Snapshot.
type WorkstateItem struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Type   string          `json:"type"`
	Status string          `json:"status"`
	Title  string          `json:"title"`
	Origin string          `json:"origin"`
	Record json.RawMessage `json:"record"`
}

// WorkstateLedgerRow is one mirrored ledger row from Snapshot.
type WorkstateLedgerRow struct {
	ID      string          `json:"id"`
	StoryID string          `json:"story_id"`
	Kind    string          `json:"kind"`
	Type    string          `json:"type"`
	Origin  string          `json:"origin"`
	Record  json.RawMessage `json:"record"`
}
