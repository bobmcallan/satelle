package api

import "encoding/json"

// Hand-written mirrors of checkout_sync.proto (package satelle.sync.v1).
// Update these structs when the proto changes. They are independently owned
// reference types — same posture as types.go — and must not import internal/.
// satelle-server copies the proto; this repo does not implement the server.

// ApplyRequest is the Sync.Apply input: a project-scoped batch of work items
// and ledger entries. Project is the hosted project id or slug. Record bytes
// on each item/entry are UTF-8 JSON (the opaque CLI payload).
type ApplyRequest struct {
	Project string        `json:"project"`
	Items   []WorkItem    `json:"items"`
	Ledger  []LedgerEntry `json:"ledger"`
}

// ApplyResponse is the Sync.Apply result: counts of upserted rows. JSON keys
// match WorkstateIngestResult so the gRPC and REST write surfaces cannot drift.
type ApplyResponse struct {
	Items  int `json:"items"`
	Ledger int `json:"ledger"`
}

// SnapshotRequest is the Sync.Snapshot input. Kind optionally filters items
// (mirrors REST ListWorkstateItems ?kind=).
type SnapshotRequest struct {
	Project string `json:"project"`
	Kind    string `json:"kind,omitempty"`
}

// SnapshotResponse is the Sync.Snapshot result: current items and ledger the
// caller may read.
type SnapshotResponse struct {
	Items  []WorkItem    `json:"items"`
	Ledger []LedgerEntry `json:"ledger"`
}

// WorkItem is one work-state row on the checkout-sync wire. JSON keys match
// WorkstateItem. Record is the opaque CLI payload (UTF-8 JSON).
type WorkItem struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Type   string          `json:"type"`
	Status string          `json:"status"`
	Title  string          `json:"title"`
	Origin string          `json:"origin"`
	Record json.RawMessage `json:"record"`
}

// LedgerEntry is one ledger row on the checkout-sync wire. JSON keys match
// WorkstateLedgerRow. Record is the opaque CLI payload (UTF-8 JSON).
type LedgerEntry struct {
	ID      string          `json:"id"`
	StoryID string          `json:"story_id"`
	Kind    string          `json:"kind"`
	Type    string          `json:"type"`
	Origin  string          `json:"origin"`
	Record  json.RawMessage `json:"record"`
}

// SyncAuthMetadata returns the gRPC metadata map every Sync RPC carries.
// The key is lowercase authorization (gRPC metadata keys are case-insensitive
// but canonically lowercase). Value is "Bearer <accessToken>". Token issue
// and refresh stay on HTTP /oauth/*; there is no gRPC login.
func SyncAuthMetadata(accessToken string) map[string]string {
	return map[string]string{"authorization": "Bearer " + accessToken}
}
