package hosted

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tempStore(t *testing.T) FileStore {
	t.Helper()
	return FileStore{Path: filepath.Join(t.TempDir(), "credentials.toml")}
}

func TestCredentialsPathXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	p, err := CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/xdg", "satelle", "credentials.toml") {
		t.Fatalf("path = %q", p)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := tempStore(t)
	in := Credential{ServerURL: "https://h/", AccessToken: "a", RefreshToken: "r", Scope: "x"}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	// Trailing-slash-insensitive lookup.
	got, err := s.Load("https://h")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "a" || got.RefreshToken != "r" {
		t.Fatalf("got %+v", got)
	}
	if got.ServerURL != "https://h" {
		t.Errorf("server_url not normalized on save: %q", got.ServerURL)
	}
}

func TestSaveLoadIdentityFields(t *testing.T) {
	s := tempStore(t)
	in := Credential{ServerURL: "https://h", AccessToken: "a", RefreshToken: "r",
		DisplayName: "Dev User", Email: "dev@x.io"}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("https://h")
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Dev User" || got.Email != "dev@x.io" {
		t.Fatalf("identity fields not round-tripped: %+v", got)
	}
}

func TestSaveUpsertByServer(t *testing.T) {
	s := tempStore(t)
	_ = s.Save(Credential{ServerURL: "https://a", AccessToken: "1", RefreshToken: "1"})
	_ = s.Save(Credential{ServerURL: "https://b", AccessToken: "2", RefreshToken: "2"})
	// Update a in place.
	_ = s.Save(Credential{ServerURL: "https://a", AccessToken: "1b", RefreshToken: "1b"})

	cf, _, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	if len(cf.Credential) != 2 {
		t.Fatalf("expected 2 credentials after upsert, got %d", len(cf.Credential))
	}
	a, _ := s.Load("https://a")
	if a.AccessToken != "1b" {
		t.Fatalf("upsert did not replace: %+v", a)
	}
}

func TestSaveRejectsIncomplete(t *testing.T) {
	s := tempStore(t)
	if err := s.Save(Credential{ServerURL: "https://h", AccessToken: "a"}); err == nil {
		t.Fatal("expected error for missing refresh token")
	}
	if err := s.Save(Credential{AccessToken: "a", RefreshToken: "r"}); err == nil {
		t.Fatal("expected error for missing server_url")
	}
}

func TestLoadMissingIsErrNoCredential(t *testing.T) {
	s := tempStore(t)
	if _, err := s.Load("https://none"); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("expected ErrNoCredential, got %v", err)
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	s := tempStore(t)
	_ = s.Save(Credential{ServerURL: "https://a", AccessToken: "1", RefreshToken: "1"})
	_ = s.Save(Credential{ServerURL: "https://b", AccessToken: "2", RefreshToken: "2"})
	if err := s.Delete("https://a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("https://a"); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("a should be gone, got %v", err)
	}
	if _, err := s.Load("https://b"); err != nil {
		t.Fatalf("b should remain: %v", err)
	}
	// Delete of an absent server is a no-op.
	if err := s.Delete("https://none"); err != nil {
		t.Fatalf("delete absent: %v", err)
	}
}

func TestSaveFilePerms0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix perms")
	}
	s := tempStore(t)
	_ = s.Save(Credential{ServerURL: "https://h", AccessToken: "a", RefreshToken: "r"})
	fi, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %o, want 600", fi.Mode().Perm())
	}
}
