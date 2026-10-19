package hosted

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// bearerAttempts scripts a WithBearer attempt: it answers statuses[i] on call i
// (with body on a 401) and records the bearer each call carried.
type bearerAttempts struct {
	statuses []int
	body     []byte
	bearers  []string
}

func (b *bearerAttempts) attempt(_ context.Context, bearer string) (int, []byte, error) {
	i := len(b.bearers)
	b.bearers = append(b.bearers, bearer)
	st := b.statuses[i]
	if st == http.StatusUnauthorized {
		return st, b.body, nil
	}
	return st, nil, nil
}

func newBearerClient(t *testing.T, store Store) (*Client, *fakeProvider) {
	t.Helper()
	prov := newFakeProvider(t)
	return NewClient("http://hosted.example", store, guardedClient(t)), prov
}

func TestWithBearerNoCredentialRunsNoAttempt(t *testing.T) {
	c, _ := newBearerClient(t, &memStore{})
	a := &bearerAttempts{}
	err := c.WithBearer(context.Background(), a.attempt)
	if !errors.Is(err, ErrLoginRequired) || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("err = %v, want ErrLoginRequired naming satelle login", err)
	}
	if len(a.bearers) != 0 {
		t.Errorf("attempt ran %d times with no credential", len(a.bearers))
	}
}

func TestWithBearerOKRunsOnce(t *testing.T) {
	store := &memStore{}
	c, prov := newBearerClient(t, store)
	_ = store.Save(prov.credential("http://hosted.example"))
	a := &bearerAttempts{statuses: []int{http.StatusSwitchingProtocols}}
	if err := c.WithBearer(context.Background(), a.attempt); err != nil {
		t.Fatal(err)
	}
	if len(a.bearers) != 1 || a.bearers[0] != "access-1" {
		t.Errorf("bearers = %v", a.bearers)
	}
}

func TestWithBearerUnknownSubjectIsFinal(t *testing.T) {
	store := &memStore{}
	c, prov := newBearerClient(t, store)
	_ = store.Save(prov.credential("http://hosted.example"))
	a := &bearerAttempts{
		statuses: []int{http.StatusUnauthorized},
		body:     []byte(`{"error":"unknown_subject","message":"` + unknownSubjectMessage + `"}`),
	}
	err := c.WithBearer(context.Background(), a.attempt)
	var us *UnknownSubjectError
	if !errors.As(err, &us) || us.Message != unknownSubjectMessage {
		t.Fatalf("err = %v, want the server's unknown_subject message", err)
	}
	if len(a.bearers) != 1 || len(prov.refreshes()) != 0 {
		t.Errorf("attempts=%d refreshes=%d, want 1 and 0", len(a.bearers), len(prov.refreshes()))
	}
}

func TestWithBearerSessionTokenIsNotRefreshed(t *testing.T) {
	store := &memStore{}
	c, prov := newBearerClient(t, store)
	_ = store.Save(Credential{ServerURL: "http://hosted.example", AccessToken: testSessionToken, TokenType: sessionTokenType})
	a := &bearerAttempts{statuses: []int{http.StatusUnauthorized}}
	err := c.WithBearer(context.Background(), a.attempt)
	if !errors.Is(err, ErrSessionTokenRefused) {
		t.Fatalf("err = %v, want ErrSessionTokenRefused", err)
	}
	if len(a.bearers) != 1 || len(prov.refreshes()) != 0 {
		t.Errorf("attempts=%d refreshes=%d, want 1 and 0", len(a.bearers), len(prov.refreshes()))
	}
}

func TestWithBearerRefreshesPersistsAndRetriesOnce(t *testing.T) {
	store := &memStore{}
	c, prov := newBearerClient(t, store)
	_ = store.Save(prov.credential("http://hosted.example"))
	a := &bearerAttempts{statuses: []int{http.StatusUnauthorized, http.StatusSwitchingProtocols}}
	if err := c.WithBearer(context.Background(), a.attempt); err != nil {
		t.Fatal(err)
	}
	if len(a.bearers) != 2 || a.bearers[0] != "access-1" || a.bearers[1] != "access-2" {
		t.Fatalf("bearers = %v, want the stale then the rotated token", a.bearers)
	}
	prov.wantRefresh(t, 0, "refresh-1")
	if got, _ := store.Load("http://hosted.example"); got.AccessToken != "access-2" || got.RefreshToken != "refresh-2" {
		t.Errorf("rotated pair not persisted: %+v", got)
	}
}

func TestWithBearerSecondRefusalIsTheCallersToRead(t *testing.T) {
	store := &memStore{}
	c, prov := newBearerClient(t, store)
	_ = store.Save(prov.credential("http://hosted.example"))
	a := &bearerAttempts{statuses: []int{http.StatusUnauthorized, http.StatusUnauthorized}}
	if err := c.WithBearer(context.Background(), a.attempt); err != nil {
		t.Fatalf("err = %v, want nil: a second 401 is read by the caller's own attempt", err)
	}
	if len(a.bearers) != 2 {
		t.Errorf("attempts = %d, want exactly one retry", len(a.bearers))
	}
}

func TestWithBearerFailedRefreshIsLoginRequired(t *testing.T) {
	store := &memStore{}
	c, prov := newBearerClient(t, store)
	prov.deny = true
	_ = store.Save(prov.credential("http://hosted.example"))
	a := &bearerAttempts{statuses: []int{http.StatusUnauthorized}}
	err := c.WithBearer(context.Background(), a.attempt)
	if !errors.Is(err, ErrLoginRequired) || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("err = %v, want ErrLoginRequired naming satelle login", err)
	}
	if len(a.bearers) != 1 {
		t.Errorf("attempts = %d, want 1 (no retry after a failed refresh)", len(a.bearers))
	}
}
