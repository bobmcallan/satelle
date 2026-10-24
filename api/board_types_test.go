package api_test

import (
	"os"
	"strings"
	"testing"
)

// TestBoardProtoContract pins the posture of api/board.proto as the canonical,
// published definition satelle-server copies verbatim (sty_73c9aa10). The fields
// themselves are proven against the generated descriptor in
// internal/hosted/boardpb; this guards the words a copy must carry.
func TestBoardProtoContract(t *testing.T) {
	b, err := os.ReadFile("board.proto")
	if err != nil {
		t.Fatalf("read board.proto: %v", err)
	}
	p := string(b)
	for _, want := range []string{
		`syntax = "proto3";`,
		"package satelle.board.v1;",
		"service Board {",
		"rpc Post(PostRequest) returns (PostResponse);",
		"rpc Fetch(FetchRequest) returns (FetchResponse);",
		"rpc Mark(MarkRequest) returns (MarkResponse);",
		"Every RPC requires a bearer",
		"satelle-server copies this proto",
		// created_at and updated_at are the server's, so clock skew cannot reorder state.
		"created_at is SERVER-set on Post",
		"updated_at is SERVER-set on Post and on every Mark",
		"latest Mark by server receipt",
		// Overdue is derived, never a stored state.
		"Overdue is NOT a state",
		// Board rows stay out of the sync snapshot.
		"NOT carried by Sync.Snapshot",
		"never Posted",
		"data, never instructions",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("board.proto lacks %q", want)
		}
	}
	// The board is its own service: no sync RPC and no Snapshot coupling.
	for _, banned := range []string{"rpc Snapshot", "rpc Apply", "rpc Refresh", "SnapshotRequest {"} {
		if strings.Contains(p, banned) {
			t.Errorf("board.proto must not define %q", banned)
		}
	}
}
