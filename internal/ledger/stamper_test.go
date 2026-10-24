package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func stampStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func TestInvocationStamperMergesKeysIntoAgentInvocationRowsOnly(t *testing.T) {
	s, _ := stampStore(t)
	var seen []AppendInput
	s.SetInvocationStamper(func(_ context.Context, in AppendInput) (map[string]string, error) {
		seen = append(seen, in)
		return map[string]string{"substrate_digest": "s1", "bindings_digest": "b1"}, nil
	})
	ctx := context.Background()
	now := time.Now()

	e, err := s.Append(ctx, AppendInput{StoryID: "sty_a", Kind: KindAgentInvocation, Actor: "reviewer", Payload: json.RawMessage(`{"from":"plan","bindings_digest":"kept"}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]string
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("stamped payload is not JSON: %s", e.Payload)
	}
	if p["from"] != "plan" || p["substrate_digest"] != "s1" {
		t.Errorf("payload = %s, want the original keys plus the stamp", e.Payload)
	}
	if p["bindings_digest"] != "kept" {
		t.Errorf("a key the payload already carries must not be overwritten, got %q", p["bindings_digest"])
	}
	if len(seen) != 1 || seen[0].StoryID != "sty_a" || seen[0].Actor != "reviewer" {
		t.Errorf("the stamper must receive the AppendInput, got %+v", seen)
	}

	// The stored row carries the stamp, not just the returned one.
	rows, err := s.ListByStory(ctx, "sty_a", KindAgentInvocation)
	if err != nil || len(rows) != 1 || string(rows[0].Payload) != string(e.Payload) {
		t.Errorf("stored payload = %v (%v), want %s", rows, err, e.Payload)
	}

	// An empty payload becomes an object holding just the stamp.
	e, err = s.Append(ctx, AppendInput{StoryID: "sty_a", Kind: KindAgentInvocation}, now)
	if err != nil {
		t.Fatal(err)
	}
	if string(e.Payload) != `{"bindings_digest":"b1","substrate_digest":"s1"}` {
		t.Errorf("empty payload stamped as %s", e.Payload)
	}

	// Other kinds, and payloads that are not objects, are left alone.
	for _, in := range []AppendInput{
		{StoryID: "sty_a", Kind: "noise", Payload: json.RawMessage(`{"x":1}`)},
		{StoryID: "sty_a", Kind: KindAgentInvocation, Payload: json.RawMessage(`[1,2]`)},
	} {
		got, err := s.Append(ctx, in, now)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Payload) != string(in.Payload) {
			t.Errorf("%s payload was changed to %s", in.Kind, got.Payload)
		}
	}
}

func TestInvocationStamperFailureNeverFailsTheAppend(t *testing.T) {
	s, _ := stampStore(t)
	s.SetInvocationStamper(func(context.Context, AppendInput) (map[string]string, error) {
		return nil, errors.New("index unavailable")
	})
	e, err := s.Append(context.Background(), AppendInput{StoryID: "sty_a", Kind: KindAgentInvocation, Payload: json.RawMessage(`{"a":1}`)}, time.Now())
	if err != nil || string(e.Payload) != `{"a":1}` {
		t.Errorf("a failing stamper must leave the row as written: %s, %v", e.Payload, err)
	}
}

func TestTransactionBoundStoreDoesNotStamp(t *testing.T) {
	s, db := stampStore(t)
	called := false
	s.SetInvocationStamper(func(context.Context, AppendInput) (map[string]string, error) {
		called = true
		return map[string]string{"substrate_digest": "s"}, nil
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := s.WithTx(tx).Append(context.Background(), AppendInput{StoryID: "sty_a", Kind: KindAgentInvocation}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("the stamper runs outside any transaction; a tx-bound store must not call it")
	}
}
