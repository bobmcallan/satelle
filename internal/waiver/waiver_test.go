package waiver

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

// plainEnv replaces the process environment with an empty one (restored on
// cleanup), so no test depends on the harness it happens to run under.
func plainEnv(t *testing.T) {
	t.Helper()
	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, e := range saved {
			k, v, _ := strings.Cut(e, "=")
			_ = os.Setenv(k, v)
		}
	})
}

func openLedger(t *testing.T) *ledger.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.Ledger
}

func ttyFile(t *testing.T, content string) TTYConfirmer {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return TTYConfirmer{Path: p, Out: io.Discard}
}

func waiverRows(t *testing.T, ls *ledger.Store) []ledger.Entry {
	t.Helper()
	rows, err := ls.List(context.Background(), ledger.ListFilter{Kind: ledger.KindGateWaiver})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func goodInput() Input {
	return Input{StoryID: "sty_a", Gate: "satelle-story-release-review", Check: "check_plan_consumed", Reason: "cannot be produced after the fact"}
}

// AC1: the real terminal confirmer against a file holding the correct id writes
// one row with every field; the same confirmer given a wrong id writes none.
func TestRecordWritesOneDeveloperRowOnlyAfterTheIdIsRetyped(t *testing.T) {
	plainEnv(t)
	ls := openLedger(t)

	w, err := Record(context.Background(), ls, goodInput(), ttyFile(t, "sty_a\n"), time.Now())
	if err != nil {
		t.Fatalf("Record with the correct id: %v", err)
	}
	rows := waiverRows(t, ls)
	if len(rows) != 1 {
		t.Fatalf("want one gate_waiver row, got %d", len(rows))
	}
	if r := rows[0]; r.Actor != "developer" || r.StoryID != "sty_a" || r.ID != w.ID {
		t.Errorf("row = actor %q story %q id %q, want developer sty_a %q", r.Actor, r.StoryID, r.ID, w.ID)
	}
	got, err := List(context.Background(), ls, "sty_a")
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if got[0].Gate != "satelle-story-release-review" || got[0].Check != "check_plan_consumed" || got[0].Reason != "cannot be produced after the fact" {
		t.Errorf("payload = %+v", got[0].Payload)
	}

	other := openLedger(t)
	if _, err := Record(context.Background(), other, goodInput(), ttyFile(t, "sty_b\n"), time.Now()); err == nil {
		t.Fatal("a wrong id must be refused")
	}
	if n := len(waiverRows(t, other)); n != 0 {
		t.Errorf("a wrong id wrote %d rows, want 0", n)
	}
}

// AC2: an agent session is refused with no row, however the terminal answers.
func TestRecordRefusesAnAgentSession(t *testing.T) {
	for _, env := range []string{"SATELLE_SESSION", "SATELLE_SCRATCH"} {
		t.Run(env, func(t *testing.T) {
			plainEnv(t)
			t.Setenv(env, "x")
			ls := openLedger(t)
			if _, err := Record(context.Background(), ls, goodInput(), ttyFile(t, "sty_a\n"), time.Now()); err == nil {
				t.Fatal("an agent session must be refused")
			}
			if n := len(waiverRows(t, ls)); n != 0 {
				t.Errorf("an agent session wrote %d rows, want 0", n)
			}
		})
	}
}

// AC2: the default confirmer against a terminal that cannot be opened refuses,
// and never falls back to stdin even when stdin holds the right id.
func TestRecordRefusesWhenNoTerminalCanBeOpened(t *testing.T) {
	plainEnv(t)
	ls := openLedger(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("sty_a\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	savedStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = savedStdin; _ = r.Close() })

	noTTY := TTYConfirmer{Path: filepath.Join(t.TempDir(), "no-tty"), Out: io.Discard}
	if _, err := Record(context.Background(), ls, goodInput(), noTTY, time.Now()); err == nil {
		t.Fatal("no terminal must be refused")
	}
	if n := len(waiverRows(t, ls)); n != 0 {
		t.Errorf("no terminal wrote %d rows, want 0", n)
	}
}

func TestRecordRefusesAMissingField(t *testing.T) {
	plainEnv(t)
	for _, mutate := range []func(*Input){
		func(i *Input) { i.Gate = "" }, func(i *Input) { i.Check = " " }, func(i *Input) { i.Reason = "" }, func(i *Input) { i.StoryID = "" },
	} {
		ls := openLedger(t)
		in := goodInput()
		mutate(&in)
		if _, err := Record(context.Background(), ls, in, ttyFile(t, "sty_a\n"), time.Now()); err == nil {
			t.Errorf("Record(%+v) must be refused", in)
		}
		if n := len(waiverRows(t, ls)); n != 0 {
			t.Errorf("Record(%+v) wrote %d rows", in, n)
		}
	}
}

// A gate_waiver row whose actor is not the developer is not a waiver.
func TestListIgnoresARowNotWrittenAsTheDeveloper(t *testing.T) {
	ls := openLedger(t)
	_, err := ls.Append(context.Background(), ledger.AppendInput{
		StoryID: "sty_a", Kind: ledger.KindGateWaiver, Actor: "executor",
		Payload: []byte(`{"gate":"g","check":"c","reason":"r"}`),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := List(context.Background(), ls, "sty_a"); len(got) != 0 {
		t.Errorf("List = %+v, want none", got)
	}
}
