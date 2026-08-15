package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestLandingRendersStandingSyncFailure(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if _, err := s.TouchPartition(ctx, "rk", "demo", time.Now()); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := s.ReplaceKind(ctx, "rk", "identity", []mirror.ItemRow{{
		ID: "meta", Payload: `{"project_name":"demo","repo_root":"` + repo + `"}`,
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordPush(config.GlobalDir(), repo, false, "hosted 505", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	ms := NewMirror(s)
	rr := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	cell := updatedCell(t, body)
	if !strings.Contains(cell, "Push Failing") {
		t.Fatalf("Push Failing tag missing from Updated cell:\n%s", cell)
	}
	if !strings.Contains(cell, `class="sync-tag-fail"`) {
		t.Fatalf("fail tag class missing from Updated cell:\n%s", cell)
	}
	for _, gone := range []string{"local:", "remote:", "pushed"} {
		if strings.Contains(cell, gone) {
			t.Fatalf("Updated cell must not contain %q:\n%s", gone, cell)
		}
	}
	if strings.Contains(body, "sync-fail-detail") {
		t.Fatal("landing must not render a sync-fail-detail row")
	}
	if strings.Contains(body, "hosted 505") {
		t.Fatalf("fail reason must not appear on the landing:\n%s", body)
	}
	if strings.Contains(body, "logged to") || strings.Contains(body, "server.log") {
		t.Fatalf("log path must not appear on the landing:\n%s", body)
	}
	if n := strings.Count(body, `tr class="row" data-slug=`); n != 1 {
		t.Fatalf("landing must have exactly one primary row per partition, got %d:\n%s", n, body)
	}
}

func updatedCell(t *testing.T, body string) string {
	t.Helper()
	const open = `<td class="updated-cell">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("updated-cell missing:\n%s", body)
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, "</td>")
	if j < 0 {
		t.Fatalf("updated-cell unclosed:\n%s", rest)
	}
	return rest[:j]
}

func TestProjectPageRendersStandingSyncFailure(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if _, err := s.TouchPartition(ctx, "rk", "demo", time.Now()); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := s.ReplaceKind(ctx, "rk", "identity", []mirror.ItemRow{{
		ID: "meta", Payload: `{"project_name":"demo","repo_root":"` + repo + `"}`,
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordPush(config.GlobalDir(), repo, false, "hosted 505", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	ms := NewMirror(s)
	rr := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/r/demo/", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `remote: <span class="sync-fail">push failing`) {
		t.Fatalf("project page push failing must follow remote::\n%s", body)
	}
	if strings.Contains(body, `title="hosted 505"`) {
		t.Fatal("reason must not live only in a title tooltip")
	}
	if !strings.Contains(body, ">hosted 505<") {
		t.Fatalf("project page reason not a visible text node:\n%s", body)
	}
	if !strings.Contains(body, "logged to") || !strings.Contains(body, "server.log") {
		t.Fatalf("project page log path missing:\n%s", body)
	}
}

func TestLandingUpdatedCellIsLocalOnly(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if _, err := s.TouchPartition(ctx, "rk", "demo", time.Now()); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := s.ReplaceKind(ctx, "rk", "identity", []mirror.ItemRow{{
		ID: "meta", Payload: `{"project_name":"demo","repo_root":"` + repo + `"}`,
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordPush(config.GlobalDir(), repo, true, "", "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ms := NewMirror(s)
	rr := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	cell := updatedCell(t, body)
	if !strings.Contains(cell, `class="rel-time"`) {
		t.Fatalf("Updated cell must carry the local ingest stamp:\n%s", cell)
	}
	for _, gone := range []string{"local:", "remote:", "pushed", "sync-ok", "Push Failing"} {
		if strings.Contains(cell, gone) {
			t.Fatalf("Updated cell must not contain %q:\n%s", gone, cell)
		}
	}
}

func TestProjectPageNamesBothTimes(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if _, err := s.TouchPartition(ctx, "rk", "demo", time.Now()); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := s.ReplaceKind(ctx, "rk", "identity", []mirror.ItemRow{{
		ID: "meta", Payload: `{"project_name":"demo","repo_root":"` + repo + `"}`,
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordPush(config.GlobalDir(), repo, true, "", "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ms := NewMirror(s)
	rr := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/r/demo/", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "local: updated <time") {
		t.Fatalf("project header missing local: updated outside time.rel-time:\n%s", body)
	}
	if !strings.Contains(body, "remote:") {
		t.Fatalf("project header missing remote: prefix:\n%s", body)
	}
	if !strings.Contains(body, ">pushed <time") {
		t.Fatalf("project header must show pushed outside time.rel-time:\n%s", body)
	}
}

func TestLandingRendersLocalOnly(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if _, err := s.TouchPartition(ctx, "rk", "demo", time.Now()); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := s.ReplaceKind(ctx, "rk", "identity", []mirror.ItemRow{{
		ID: "meta", Payload: `{"project_name":"demo","repo_root":"` + repo + `"}`,
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordPush(config.GlobalDir(), repo, true, "", "local", time.Now()); err != nil {
		t.Fatal(err)
	}
	ms := NewMirror(s)
	rr := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	cell := updatedCell(t, body)
	if !strings.Contains(cell, `class="rel-time"`) {
		t.Fatalf("Updated cell must carry the local ingest stamp:\n%s", cell)
	}
	if strings.Contains(cell, "sync-local") || strings.Contains(cell, "remote:") {
		t.Fatalf("landing Updated cell must not show the local-only remote marker:\n%s", cell)
	}
}

func TestLandingOmitsSyncMarkerWhenNoState(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if _, err := s.TouchPartition(ctx, "rk", "demo", time.Now()); err != nil {
		t.Fatal(err)
	}
	ms := NewMirror(s)
	rr := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	if strings.Contains(body, "sync-fail") {
		t.Fatalf("no-state partition must not show a failure marker:\n%s", body)
	}
}
