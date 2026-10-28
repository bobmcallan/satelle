package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/waiver"
)

// plainCaller removes every agent marker from the environment for the test, so
// the command sees the developer's shell, whatever harness the suite runs under.
func plainCaller(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if k == config.ScratchEnv || agentcli.IsSessionEnvKey(k) {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
}

func waiveRepo(t *testing.T) (storyID string) {
	t.Helper()
	editStateRepo(t, "plan", "plan", false)
	plainCaller(t)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	leases, err := db.Leases.List(context.Background())
	if err != nil || len(leases) != 1 {
		t.Fatalf("leases: %v n=%d", err, len(leases))
	}
	return leases[0].ItemID
}

func useWaiveConfirmer(t *testing.T, c waiver.Confirmer) {
	t.Helper()
	saved := waiveConfirmer
	t.Cleanup(func() { waiveConfirmer = saved })
	waiveConfirmer = c
}

func ttyHolding(t *testing.T, content string) waiver.TTYConfirmer {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return waiver.TTYConfirmer{Path: p, Out: io.Discard}
}

func waiveArgs(id string) []string {
	return []string{"story", "waive", id, "--gate", "satelle-story-release-review", "--check", "check_plan_consumed", "--reason", "cannot be produced after the fact"}
}

// sty_bef97244 AC1: the real terminal confirmer against a file holding the right
// id records one developer row with every field; a wrong id records none.
func TestStoryWaiveRecordsOnlyAfterTheIdIsRetyped(t *testing.T) {
	id := waiveRepo(t)

	useWaiveConfirmer(t, ttyHolding(t, "sty_wrong\n"))
	if out, err := runRoot(t, waiveArgs(id)...); err == nil {
		t.Fatalf("a wrong id must be refused:\n%s", out)
	}
	if n := len(ledgerRows(t, "gate_waiver")); n != 0 {
		t.Fatalf("a wrong id wrote %d rows, want 0", n)
	}

	useWaiveConfirmer(t, ttyHolding(t, id+"\n"))
	if out, err := runRoot(t, waiveArgs(id)...); err != nil {
		t.Fatalf("the correct id must record the waiver: %v\n%s", err, out)
	}
	rows := ledgerRows(t, "gate_waiver")
	if len(rows) != 1 {
		t.Fatalf("want one gate_waiver row, got %d", len(rows))
	}
	var p waiver.Payload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if rows[0].Actor != "developer" || rows[0].StoryID != id ||
		p.Gate != "satelle-story-release-review" || p.Check != "check_plan_consumed" || p.Reason != "cannot be produced after the fact" {
		t.Errorf("row = actor %q story %q payload %+v", rows[0].Actor, rows[0].StoryID, p)
	}
}

// sty_bef97244 AC2: an agent session is refused, and so is a run whose terminal
// cannot be opened, even with the right id waiting on stdin; neither writes a row.
func TestStoryWaiveRefusesAnAgentAndAMissingTerminal(t *testing.T) {
	id := waiveRepo(t)

	t.Run("agent session", func(t *testing.T) {
		useWaiveConfirmer(t, ttyHolding(t, id+"\n"))
		t.Setenv(config.SessionEnv, "sess-agent")
		if out, err := runRoot(t, waiveArgs(id)...); err == nil {
			t.Fatalf("an agent session must be refused:\n%s", out)
		}
		if n := len(ledgerRows(t, "gate_waiver")); n != 0 {
			t.Errorf("an agent session wrote %d rows", n)
		}
	})

	t.Run("no terminal", func(t *testing.T) {
		useWaiveConfirmer(t, waiver.TTYConfirmer{Path: filepath.Join(t.TempDir(), "no-tty"), Out: io.Discard})
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.WriteString(id + "\n")
		_ = w.Close()
		saved := os.Stdin
		os.Stdin = r
		t.Cleanup(func() { os.Stdin = saved; _ = r.Close() })
		if out, err := runRoot(t, waiveArgs(id)...); err == nil {
			t.Fatalf("no controlling terminal must be refused:\n%s", out)
		}
		if n := len(ledgerRows(t, "gate_waiver")); n != 0 {
			t.Errorf("no terminal wrote %d rows", n)
		}
	})
}

func TestStoryWaiveRefusesAnUnknownStory(t *testing.T) {
	waiveRepo(t)
	useWaiveConfirmer(t, ttyHolding(t, "sty_nope\n"))
	if out, err := runRoot(t, waiveArgs("sty_nope")...); err == nil {
		t.Fatalf("a story that does not exist must be refused:\n%s", out)
	}
	if n := len(ledgerRows(t, "gate_waiver")); n != 0 {
		t.Errorf("wrote %d rows for an unknown story", n)
	}
}
