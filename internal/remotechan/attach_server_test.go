package remotechan

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// AttachServerSession (sty_7028d996): local's end of a session satelle.dev
// minted. Admission is by the node's published key, never by an enroll token.

func publishedKey(pub []byte) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) { return pub, nil }
}

type attachResult struct {
	s   *Session
	err error
}

func attachAsync(t *testing.T, h *harness, session string, keyFn func(context.Context) ([]byte, error)) <-chan attachResult {
	t.Helper()
	out := make(chan attachResult, 1)
	ctx := testCtx(t)
	go func() {
		s, err := AttachServerSession(ctx, h.client, h.relay.URL(), session, keyFn, h.opts()...)
		out <- attachResult{s, err}
	}()
	return out
}

// joinAsync starts the node once the starter's end is on the relay: the stand-in
// relay drops a frame sent while its peer is absent.
func joinAsync(t *testing.T, h *harness, session string, id *Identity, enroll string) <-chan joined {
	t.Helper()
	eventually(t, "the starter on the relay", func() bool { return h.relay.Upgrades() >= 1 })
	out := make(chan joined, 1)
	ctx := testCtx(t)
	go func() {
		s, err := Join(ctx, h.relay.URL(), session, id, enroll, h.opts()...)
		out <- joined{s, err}
	}()
	return out
}

// (a) A node whose handshake key equals the published key is admitted: no token
// was minted, and whatever enroll value it sends is ignored.
func TestAttachServerSessionAdmitsThePublishedKey(t *testing.T) {
	h := newHarness(t, true)
	id, _ := NewIdentity()
	const session = "nod_server_minted"

	att := attachAsync(t, h, session, publishedKey(id.Public()))
	rem := joinAsync(t, h, session, id, "any-bearer-the-server-ignores")
	a, r := <-att, <-rem
	if a.err != nil || r.err != nil {
		t.Fatalf("attach = %v, join = %v; want both admitted", a.err, r.err)
	}
	t.Cleanup(func() { a.s.Close(); r.s.Close() })
	if a.s.Role() != RoleLocal || r.s.Role() != RoleRemote {
		t.Fatalf("roles = %v, %v", a.s.Role(), r.s.Role())
	}
	if h.tokens.Outstanding() != 0 {
		t.Errorf("Outstanding = %d, want 0: the server-session path mints no token", h.tokens.Outstanding())
	}
	if len(h.refusals(t)) != 0 {
		t.Errorf("a clean attach recorded refusals: %v", h.refusals(t))
	}
}

// (b) A node holding a different key is refused key_mismatch and the session is
// closed; nothing it sent is delivered.
func TestAttachServerSessionRefusesAnotherKey(t *testing.T) {
	h := newHarness(t, true)
	published, _ := NewIdentity()
	impostor, _ := NewIdentity()
	const session = "nod_impostor"

	att := attachAsync(t, h, session, publishedKey(published.Public()))
	rem := joinAsync(t, h, session, impostor, "bearer")
	a, r := <-att, <-rem
	if !errors.Is(a.err, ErrKeyMismatch) || a.s != nil {
		t.Fatalf("attach = %v, %v; want ErrKeyMismatch and no session", a.s, a.err)
	}
	if r.err == nil || r.s != nil {
		t.Fatalf("impostor join = %v, %v; want an error and no session", r.s, r.err)
	}
	if got := h.refusals(t); !slices.Equal(got, []string{"key_mismatch"}) {
		t.Errorf("recorded refusals = %v, want [key_mismatch]", got)
	}
}

// (c) A key never published is refused key_unpublished within the bound, with no
// session returned.
func TestAttachServerSessionRefusesAnUnpublishedKey(t *testing.T) {
	h := newHarness(t, true)
	var asked atomic.Int32
	att := attachAsync(t, h, "nod_silent", func(context.Context) ([]byte, error) {
		asked.Add(1)
		return nil, nil
	})
	h.clock.waitTickers(t, 1)
	h.clock.Advance(EnrollTTL + time.Second)
	a := <-att
	if !errors.Is(a.err, ErrKeyUnpublished) || a.s != nil {
		t.Fatalf("attach = %v, %v; want ErrKeyUnpublished and no session", a.s, a.err)
	}
	if asked.Load() < 2 {
		t.Errorf("key asked %d times, want it polled until the bound", asked.Load())
	}
	if got := h.refusals(t); !slices.Equal(got, []string{"key_unpublished"}) {
		t.Errorf("recorded refusals = %v, want [key_unpublished]", got)
	}
}

// (d) A node that connects before its key is published is not trusted: local
// sends nothing and admits nothing until the key is present, and then only on
// the pinned key.
func TestAttachServerSessionTrustsNothingBeforeTheKey(t *testing.T) {
	h := newHarness(t, true)
	id, _ := NewIdentity()
	const session = "nod_early"
	var published atomic.Bool
	keyFn := func(context.Context) ([]byte, error) {
		if published.Load() {
			return id.Public(), nil
		}
		return nil, nil
	}

	att := attachAsync(t, h, session, keyFn)
	rem := joinAsync(t, h, session, id, "bearer")
	h.clock.waitTickers(t, 1)
	time.Sleep(100 * time.Millisecond) // the node's key half and enrollment are on the wire
	select {
	case a := <-att:
		t.Fatalf("attach returned %v, %v before the key was published", a.s, a.err)
	case r := <-rem:
		t.Fatalf("join returned %v, %v before the key was published", r.s, r.err)
	default:
	}
	for _, f := range h.relay.Frames() {
		if f.From == "local" {
			t.Fatalf("local sent a frame before the node's key was published")
		}
	}

	published.Store(true)
	h.clock.Advance(keyPollInterval)
	a, r := <-att, <-rem
	if a.err != nil || r.err != nil {
		t.Fatalf("attach = %v, join = %v after the key was published", a.err, r.err)
	}
	t.Cleanup(func() { a.s.Close(); r.s.Close() })
}

// Open's token path is unchanged behind the admit split: a valid token is
// admitted and consumed, an unknown one refused with today's reason.
func TestOpenTokenAdmissionIsUnchangedByTheSplit(t *testing.T) {
	h := newHarness(t, true)
	_, _, spec := h.connect(t)
	if err := h.tokens.Redeem(spec.Token); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("redeeming the enrolled token again = %v, want ErrTokenUsed", err)
	}

	id, _ := NewIdentity()
	l, res, _ := h.launcher(t, id, id.Public(), func(LaunchSpec) string { return "never-minted" })
	if _, err := Open(testCtx(t), h.client, h.relay.URL(), l, h.opts()...); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("unknown token: err = %v, want ErrTokenUnknown", err)
	}
	if r := <-res; !errors.Is(r.err, ErrEnrollRefused) {
		t.Fatalf("remote saw %v, want ErrEnrollRefused", r.err)
	}
}
