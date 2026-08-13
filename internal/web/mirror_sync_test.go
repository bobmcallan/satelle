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
	if !strings.Contains(body, "sync-fail") || !strings.Contains(body, "hosted 505") {
		t.Fatalf("standing failure not rendered:\n%s", body)
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
