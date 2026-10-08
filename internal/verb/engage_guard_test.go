package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestEngageGuardRefusesBeforeWrongHolder (sty_6ed6318d AC3): a session token
// whose user is not yet resolved has an empty holder, which refuseWrongHolder
// lets through; the engage guard refuses it first. Once the holder is known the
// guard passes and the wrong-holder refusal applies as for any account.
func TestEngageGuardRefusesBeforeWrongHolder(t *testing.T) {
	withWiring(t)
	db := wire(t)

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "Held"}), &story); err != nil {
		t.Fatal(err)
	}
	other := "u_other"
	if _, err := db.Stories.Update(context.Background(), story.ID, workitem.UpdateInput{Assignee: &other}, time.Now()); err != nil {
		t.Fatal(err)
	}
	engage := func() error {
		_, err := verb.Dispatch(context.Background(), "story-set", mustJSON(t, map[string]any{"id": story.ID, "status": "in_progress"}))
		return err
	}

	// Unknown: no holder, and the guard refuses with its own message.
	verb.SetAssigneeResolver(func() string { return "" })
	verb.SetEngageGuard(func(context.Context) error { return errors.New("session token, user not yet resolved") })
	if err := engage(); err == nil || !strings.Contains(err.Error(), "user not yet resolved") {
		t.Fatalf("engage with an unresolved session: err = %v", err)
	}

	// Known, and not the assignee: the guard passes, the wrong-holder refusal applies.
	verb.SetAssigneeResolver(func() string { return "u_123" })
	verb.SetEngageGuard(func(context.Context) error { return nil })
	if err := engage(); err == nil || !strings.Contains(err.Error(), "assigned to u_other") {
		t.Fatalf("engage as u_123: err = %v", err)
	}

	// Known, and the assignee: engages.
	verb.SetAssigneeResolver(func() string { return "u_other" })
	if err := engage(); err != nil {
		t.Fatalf("assignee should engage: %v", err)
	}
}
