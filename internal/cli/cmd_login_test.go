package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

func testCmd() (*cobra.Command, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	c := &cobra.Command{}
	c.SetOut(buf)
	c.SetContext(context.Background())
	return c, buf
}

func TestRunLogoutClearsCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const server = "https://logout.example"
	store := hosted.FileStore{}
	if err := store.Save(hosted.Credential{ServerURL: server, AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	cmd, _ := testCmd()
	if err := runLogout(cmd, server); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(server); !errors.Is(err, hosted.ErrNoCredential) {
		t.Fatalf("credential not cleared: %v", err)
	}
}

func TestRunWhoamiNotSignedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	cmd, _ := testCmd()
	err := runWhoami(cmd, ts.URL)
	if !errors.Is(err, hosted.ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}

func TestRunWhoamiHappy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(hosted.Principal{ID: "u", Email: "me@x.io", DisplayName: "Me", Role: "admin"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if err := (hosted.FileStore{}).Save(hosted.Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	cmd, buf := testCmd()
	if err := runWhoami(cmd, ts.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "me@x.io") || !strings.Contains(buf.String(), "admin") {
		t.Fatalf("identity not printed: %q", buf.String())
	}
}
