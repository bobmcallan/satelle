package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
)

// TestRetrieveRoundTrip (AC2): a known blob comes back byte-for-byte via
// `satelle retrieve <hash>`, with no trailing newline or framing.
func TestRetrieveRoundTrip(t *testing.T) {
	tempRepo(t)

	out, err := runRoot(t, "story", "create", "--title", "retrieve fixture", "--status", "backlog")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("parse create: %v\n%s", err, out)
	}

	content := []byte("the exact original bytes\x00\xff")
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ref, err := db.Retrieve.Put(context.Background(), created.ID, content, time.Now())
	if err != nil {
		db.Close()
		t.Fatalf("put: %v", err)
	}
	db.Close()

	got, err := runRoot(t, "retrieve", ref.Hash)
	if err != nil {
		t.Fatalf("retrieve: %v\n%s", err, got)
	}
	if got != string(content) {
		t.Fatalf("retrieve output = %q, want %q", got, string(content))
	}
}

// TestRetrieveUnknownHash (AC2): an unknown (but well-formed) hash exits
// non-zero with a clear message.
func TestRetrieveUnknownHash(t *testing.T) {
	tempRepo(t)
	out, err := runRoot(t, "retrieve", "000000000000000000000000")
	if err == nil {
		t.Fatalf("expected non-zero exit for unknown hash, got allow:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no stored original for hash") {
		t.Errorf("error = %q, want it to name the missing hash", err.Error())
	}
}

// TestRetrieveMalformedHash (AC2): a hash that is not 24 lowercase hex chars
// is refused the same way an unknown hash is.
func TestRetrieveMalformedHash(t *testing.T) {
	tempRepo(t)
	for _, bad := range []string{"not-a-hash", "abc", strings.Repeat("g", 24)} {
		out, err := runRoot(t, "retrieve", bad)
		if err == nil {
			t.Fatalf("retrieve %q: expected non-zero exit, got allow:\n%s", bad, out)
		}
	}
}
