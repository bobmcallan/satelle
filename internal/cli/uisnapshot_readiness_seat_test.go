package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/web"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_a42f4add AC5: the seat a readiness round makes effectively in flight —
// inserted when the story holds none, or flagged on a settled one — is pushed by
// listSeatsJSON as in flight with the dispatch's agent detail, and the web page
// renders the running indicator from it.
func TestReadinessSeatPushedAndRenderedAsRunning(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "inserted seat"
		if existing {
			name = "flagged settled seat"
		}
		t.Run(name, func(t *testing.T) {
			e := closeGuardEnv(t)
			ctx := context.Background()
			sty, err := e.db.Stories.Create(ctx, workitem.CreateInput{
				Kind: workitem.KindStory, Title: "Readied", Body: "goal",
				AcceptanceCriteria: "1. x", Category: "chore", Status: workitem.StatusBacklog,
			}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			owner := lease.ResolveOwner()
			if existing {
				if _, _, _, err := e.db.Leases.Acquire(ctx, sty.ID, "story", owner, "backlog", true); err != nil {
					t.Fatal(err)
				}
				if err := e.db.Leases.Confirm(ctx, sty.ID, "backlog"); err != nil {
					t.Fatal(err)
				}
				if marked, err := e.db.Leases.MarkInFlightIfSettled(ctx, sty.ID, time.Now()); err != nil || !marked {
					t.Fatalf("mark: %v %v", marked, err)
				}
			} else if ins, err := e.db.Leases.InsertIfAbsent(ctx, lease.AcquireOpts{ItemID: sty.ID, Kind: "story", Owner: owner, State: "backlog", SeatKey: sty.ID}); err != nil || !ins {
				t.Fatalf("insert: %v %v", ins, err)
			}
			if err := e.db.Leases.SetActivityDetail(ctx, sty.ID, lease.ActivityDetail{Agent: "planner", Model: "opus", Pid: 42, EventLabel: "tool: Read", EventAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}

			a := &app.App{Config: config.Config{}, RepoRoot: e.repo, Store: e.db}
			seats, err := listSeatsJSON(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			var pushed map[string]any
			for _, raw := range seats {
				var row map[string]any
				if json.Unmarshal(raw, &row) == nil && row["id"] == sty.ID {
					pushed = row
				}
			}
			if pushed == nil || pushed["in_flight"] != true || pushed["agent"] != "planner" || pushed["stale"] != false {
				t.Fatalf("pushed seat = %v, want in flight, not stale, agent planner", pushed)
			}

			// Through the mirror ingest and the page, as the web service does it.
			snap, err := buildUISnapshot(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			ms, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ms.Close() })
			mux := http.NewServeMux()
			(&mirror.IngestHandler{Store: ms}).Mount(mux)
			body, _ := json.Marshal(snap)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ingest/snapshot", bytes.NewReader(body)))
			if rec.Code != http.StatusOK {
				t.Fatalf("ingest = %d: %s", rec.Code, rec.Body)
			}
			srv := httptest.NewServer(web.NewMirror(ms).Handler)
			t.Cleanup(srv.Close)
			resp, err := http.Get(srv.URL + "/r/" + partitionSlug(a) + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			page, _ := io.ReadAll(resp.Body)
			for _, want := range []string{`class="dispatch-indicator"`, "planner (opus) running"} {
				if !strings.Contains(string(page), want) {
					t.Errorf("page missing %q for the readied story", want)
				}
			}
		})
	}
}
