package remotechan

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
)

// AC1: a reused, expired or unminted token is refused at the channel and each
// refusal is recorded once.
func TestEnrollmentRefusalsAreRecorded(t *testing.T) {
	h := newHarness(t, true)
	_, _, first := h.connect(t) // consumes the first token from the shared book

	id, _ := NewIdentity()
	open := func(present func(LaunchSpec) string) (joined, error) {
		l, res, _ := h.launcher(t, id, id.Public(), present)
		_, err := Open(testCtx(t), h.client, h.relay.URL(), l, h.opts()...)
		return <-res, err
	}

	remote, err := open(func(LaunchSpec) string { return first.Token })
	if !errors.Is(err, ErrTokenUsed) || !errors.Is(remote.err, ErrEnrollRefused) {
		t.Fatalf("reused token: local %v, remote %v; want ErrTokenUsed and ErrEnrollRefused", err, remote.err)
	}
	if remote.s != nil {
		t.Error("a remote with a reused token was admitted")
	}

	_, err = open(func(LaunchSpec) string { return "never-minted" })
	if !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("unminted token: local %v, want ErrTokenUnknown", err)
	}

	_, err = open(func(s LaunchSpec) string {
		h.clock.Advance(EnrollTTL + time.Second)
		return s.Token
	})
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token: local %v, want ErrTokenExpired", err)
	}

	// Rows stamped with the same fake instant have no defined order: compare sets.
	got := h.refusals(t)
	sort.Strings(got)
	if want := []string{"token_expired", "token_unknown", "token_used"}; !slices.Equal(got, want) {
		t.Fatalf("recorded refusals = %v, want exactly one each of %v", got, want)
	}
}

// AC1: the token is minted per start and consumed by the enrollment.
func TestOpenMintsOneTokenAndConsumesIt(t *testing.T) {
	h := newHarness(t, true)
	_, _, spec := h.connect(t)
	if spec.Token == "" || spec.Session == "" || spec.RelayURL != h.relay.URL() {
		t.Fatalf("launch spec = %+v", spec)
	}
	if h.tokens.Outstanding() != 0 {
		t.Errorf("Outstanding = %d after enrollment, want 0", h.tokens.Outstanding())
	}
	if err := h.tokens.Redeem(spec.Token); !errors.Is(err, ErrTokenUsed) {
		t.Errorf("redeeming the enrolled token again = %v, want ErrTokenUsed", err)
	}
	if len(h.refusals(t)) != 0 {
		t.Errorf("a clean enrollment recorded refusals: %v", h.refusals(t))
	}
}

// AC2: the key read at launch is the key pinned; a different key is refused and
// nothing the impostor sent is delivered.
func TestSubstitutedKeyRefused(t *testing.T) {
	h := newHarness(t, true)
	real, _ := NewIdentity()
	impostor, _ := NewIdentity()
	l, res, _ := h.launcher(t, impostor, real.Public(), func(s LaunchSpec) string { return s.Token })

	s, err := Open(testCtx(t), h.client, h.relay.URL(), l, h.opts()...)
	if !errors.Is(err, ErrKeyMismatch) || s != nil {
		t.Fatalf("Open = %v, %v; want ErrKeyMismatch and no session", s, err)
	}
	if r := <-res; r.err == nil || r.s != nil {
		t.Fatalf("impostor Join = %v, %v; want an error and no session", r.s, r.err)
	}
	// The relay forwarded the impostor's key half and its (unreadable) enrollment;
	// local answered with its own key half and nothing else — no admission.
	var fromLocal int
	for _, f := range h.relay.Frames() {
		if f.From == "local" {
			fromLocal++
		}
	}
	if fromLocal != 1 {
		t.Errorf("local sent %d frames, want 1 (its key half only)", fromLocal)
	}
	if got := h.refusals(t); len(got) != 1 || got[0] != "key_mismatch" {
		t.Errorf("recorded refusals = %v, want [key_mismatch]", got)
	}
	// The token was never redeemed by the impostor, so it is still outstanding.
	if h.tokens.Outstanding() != 1 {
		t.Errorf("Outstanding = %d, want the unspent token", h.tokens.Outstanding())
	}
}

func TestPinnedKeyAccepted(t *testing.T) {
	h := newHarness(t, true)
	local, remote, _ := h.connect(t)
	if local.Role() != RoleLocal || remote.Role() != RoleRemote {
		t.Fatalf("roles = %v, %v", local.Role(), remote.Role())
	}
}

// AC3: every byte the relay forwards is free of the payload, the enrollment
// token and the message field names, and both ends read the payloads exactly.
func TestRelaySeesNoPlaintext(t *testing.T) {
	h := newHarness(t, true)
	local, remote, spec := h.connect(t)
	ctx := testCtx(t)

	const marker = "ZEBRA-MARKER-7f3a91"
	toRemote := []byte(`{"secret":"` + marker + `-to-remote","cmd":"deploy"}`)
	toLocal := []byte(`{"secret":"` + marker + `-to-local","result":"ok"}`)
	if err := local.Send(ctx, "job-1", toRemote); err != nil {
		t.Fatal(err)
	}
	if err := remote.Send(ctx, "job-1", toLocal); err != nil {
		t.Fatal(err)
	}
	gotRemote, err := remote.Recv(ctx)
	if err != nil || !bytes.Equal(gotRemote.Body, toRemote) || gotRemote.Job != "job-1" {
		t.Fatalf("remote received %+v, %v", gotRemote, err)
	}
	gotLocal, err := local.Recv(ctx)
	if err != nil || !bytes.Equal(gotLocal.Body, toLocal) || gotLocal.Job != "job-1" {
		t.Fatalf("local received %+v, %v", gotLocal, err)
	}

	frames := h.relay.Frames()
	var fromLocal, fromRemote int
	for _, f := range frames {
		switch f.From {
		case "local":
			fromLocal++
		case "remote":
			fromRemote++
		}
		for _, clear := range []string{
			marker, spec.Token, "secret", "cmd", "deploy", "result", "job-1",
			`"t"`, "enroll", "admit", "token", "body", `"job"`,
		} {
			if bytes.Contains(f.Data, []byte(clear)) {
				t.Errorf("relay saw %q in clear in a frame from %s: %x", clear, f.From, f.Data)
			}
		}
	}
	// local: key half + admit + data; remote: key half + enroll + data.
	if fromLocal < 3 || fromRemote < 3 {
		t.Errorf("relay tapped %d local and %d remote frames; the check saw too little", fromLocal, fromRemote)
	}
}

// Frames out of sequence (a replay, or a dropped frame) end the session.
func TestReplayedFrameEndsTheSession(t *testing.T) {
	h := newHarness(t, true)
	local, remote, _ := h.connect(t)
	ctx := testCtx(t)
	if err := local.Send(ctx, "", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Recv(ctx); err != nil {
		t.Fatal(err)
	}
	// Replay the frame sealed at seq 1 by rewinding the sender and sending again.
	local.wmu.Lock()
	local.send.seq--
	local.wmu.Unlock()
	_ = local.Send(ctx, "", []byte("one"))
	select {
	case <-remote.Done():
	case <-ctx.Done():
		t.Fatal("remote kept a session that was fed a replayed frame")
	}
	if !errors.Is(remote.Err(), ErrFrame) {
		t.Errorf("remote.Err = %v, want ErrFrame", remote.Err())
	}
}

// AC6: signed out, Open refuses with the sign-in it needs and touches nothing.
func TestOpenRequiresSignIn(t *testing.T) {
	h := newHarness(t, false)
	launched := false
	l := LauncherFunc(func(context.Context, LaunchSpec) ([]byte, error) { launched = true; return nil, nil })
	_, err := Open(testCtx(t), h.client, h.relay.URL(), l, h.opts()...)
	if !errors.Is(err, hosted.ErrLoginRequired) || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("Open = %v, want ErrLoginRequired naming satelle login", err)
	}
	if h.relay.Attempts() != 0 {
		t.Errorf("relay saw %d connection attempts, want 0", h.relay.Attempts())
	}
	if launched || h.tokens.Outstanding() != 0 {
		t.Errorf("launched=%v outstanding tokens=%d; a signed-out Open must mint and launch nothing", launched, h.tokens.Outstanding())
	}
}

func TestSignedInBearerReachesRelay(t *testing.T) {
	h := newHarness(t, true)
	h.connect(t)
	if got := h.relay.Bearers(); len(got) != 1 || got[0] != "access-1" {
		t.Fatalf("relay bearers = %v, want [access-1]", got)
	}
}

// AC6: a stale bearer goes through hosted's one refresh path and the retried
// upgrade carries the rotated token.
func TestRelayBearerRefreshSucceeds(t *testing.T) {
	h := newHarness(t, true)
	h.relay.RejectBearers(1)
	h.connect(t)
	if got := h.relay.Bearers(); len(got) != 2 || got[0] != "access-1" || got[1] != "access-2" {
		t.Fatalf("relay bearers = %v, want the stale then the rotated token", got)
	}
	if h.idp.refreshes() != 1 {
		t.Errorf("refreshes = %d, want 1", h.idp.refreshes())
	}
	if c, _ := h.store.Load(harnessServer); c.AccessToken != "access-2" || c.RefreshToken != "refresh-2" {
		t.Errorf("rotated credential not persisted: %+v", c)
	}
}

func TestRelayBearerRefreshFails(t *testing.T) {
	h := newHarness(t, true)
	h.relay.RejectBearers(1)
	h.idp.deny = true
	launched := false
	l := LauncherFunc(func(context.Context, LaunchSpec) ([]byte, error) { launched = true; return nil, nil })
	_, err := Open(testCtx(t), h.client, h.relay.URL(), l, h.opts()...)
	if !errors.Is(err, hosted.ErrLoginRequired) || !strings.Contains(err.Error(), "satelle login") {
		t.Fatalf("Open = %v, want ErrLoginRequired naming satelle login", err)
	}
	if h.relay.Attempts() != 1 {
		t.Errorf("upgrade attempted %d times, want exactly once", h.relay.Attempts())
	}
	if launched || h.tokens.Outstanding() != 0 {
		t.Errorf("launched=%v outstanding=%d after a failed sign-in", launched, h.tokens.Outstanding())
	}
}

// A relay that keeps answering 401 after the refresh is a relay refusal, not a loop.
func TestRelayStill401AfterRefreshIsRefused(t *testing.T) {
	h := newHarness(t, true)
	h.relay.RejectBearers(2)
	_, err := Open(testCtx(t), h.client, h.relay.URL(), LauncherFunc(func(context.Context, LaunchSpec) ([]byte, error) {
		t.Fatal("launched after the relay refused")
		return nil, nil
	}), h.opts()...)
	if !errors.Is(err, ErrRelayRefused) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Open = %v, want ErrRelayRefused (HTTP 401)", err)
	}
	if h.relay.Attempts() != 2 {
		t.Errorf("attempts = %d, want the upgrade and one retry", h.relay.Attempts())
	}
}

// The launched remote's upgrade can reach the relay before it has recorded the
// local end whose 101 it just wrote; Open still pairs with it.
func TestOpenPairsWhenRemoteArrivesDuringLocalRegistration(t *testing.T) {
	h := newHarness(t, true)
	h.relay.HoldLocalUntilRemote()
	local, remote, _ := h.connect(t)
	ctx := testCtx(t)
	if err := local.Send(ctx, "", []byte("one")); err != nil {
		t.Fatalf("local Send: %v", err)
	}
	if m, err := remote.Recv(ctx); err != nil || string(m.Body) != "one" {
		t.Fatalf("remote Recv = %q, %v; want one", m.Body, err)
	}
}

// AC9: the remote's upgrade carries the node token as its bearer.
func TestJoinCarriesTheNodeTokenAsBearer(t *testing.T) {
	h := newHarness(t, true)
	_, _, spec := h.connect(t)
	if got := h.relay.RemoteBearers(); len(got) != 1 || got[0] != spec.Token {
		t.Fatalf("remote bearers = %v, want [%s]", got, spec.Token)
	}
}

// AC9: a remote is admitted into its session with no local end waiting.
func TestJoinIsAdmittedWithoutAWaitingLocal(t *testing.T) {
	h := newHarness(t, true)
	id, _ := NewIdentity()
	ctx, cancel := context.WithCancel(testCtx(t))
	done := make(chan error, 1)
	go func() {
		_, err := Join(ctx, h.relay.URL(), "no-local-yet", id, "node-token", h.opts()...)
		done <- err
	}()
	eventually(t, "the relay to admit the remote", func() bool { return h.relay.Upgrades() == 1 })
	if got := h.relay.RemoteBearers(); len(got) != 1 || got[0] != "node-token" {
		t.Errorf("remote bearers = %v, want [node-token]", got)
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("Join without a local end must not return a session")
	}
}

// AC9: a relay that does not accept the node token answers 401, and Join says so
// as a status a caller can tell from any other refusal.
func TestJoinRefusedTokenIsA401(t *testing.T) {
	h := newHarness(t, true)
	h.relay.RejectRemoteBearers(1)
	id, _ := NewIdentity()
	_, err := Join(testCtx(t), h.relay.URL(), "s", id, "node-token", h.opts()...)
	var rs *RelayStatusError
	if !errors.Is(err, ErrRelayRefused) || !errors.As(err, &rs) || rs.Status != 401 || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Join = %v, want ErrRelayRefused with status 401", err)
	}
}
