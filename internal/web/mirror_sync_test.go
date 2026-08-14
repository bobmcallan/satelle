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
	if !strings.Contains(body, "sync-fail") {
		t.Fatalf("standing failure chip missing:\n%s", body)
	}
	if !strings.Contains(body, `remote: <span class="sync-fail">push failing`) {
		t.Fatalf("push failing must follow remote::\n%s", body)
	}
	if strings.Contains(body, `title="hosted 505"`) {
		t.Fatal("reason must not live only in a title tooltip")
	}
	if !strings.Contains(body, ">hosted 505<") {
		t.Fatalf("reason not a visible text node:\n%s", body)
	}
	if !strings.Contains(body, "logged to") || !strings.Contains(body, "server.log") {
		t.Fatalf("log path missing:\n%s", body)
	}
	if n := strings.Count(body, `tr class="row" data-slug=`); n != 1 {
		t.Fatalf("landing must have exactly one primary row per partition, got %d:\n%s", n, body)
	}
	if strings.Contains(body, `tr class="row sync-fail-detail"`) || strings.Contains(body, `sync-fail-detail" data-slug=`) {
		t.Fatal("failure detail row must not carry class=row or data-slug")
	}
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

func TestLandingRendersLastSuccessfulPush(t *testing.T) {
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
	if !strings.Contains(body, "sync-ok") || !strings.Contains(body, "pushed") {
		t.Fatalf("successful push not rendered:\n%s", body)
	}
	if !strings.Contains(body, "local: updated") || !strings.Contains(body, "remote:") {
		t.Fatalf("landing Updated cell missing local:/remote: prefixes:\n%s", body)
	}
	if !strings.Contains(body, ">pushed <time") {
		t.Fatalf("pushed must sit outside time.rel-time so the ticker cannot strip it:\n%s", body)
	}
	if strings.Contains(body, `class="rel-time sync-ok"`) {
		t.Fatal("sync-ok must wrap the label, not live on the time element")
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
	if !strings.Contains(body, "sync-local") {
		t.Fatalf("local-only marker not rendered:\n%s", body)
	}
	if !strings.Contains(body, `remote: <span class="sync-local"`) {
		t.Fatalf("local-only marker must follow remote::\n%s", body)
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
