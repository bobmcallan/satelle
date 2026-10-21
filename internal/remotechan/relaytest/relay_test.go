package relaytest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dial(ctx context.Context, r *Relay, session, role string) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, r.URL()+"/"+session+"?role="+role, &websocket.DialOptions{
		HTTPClient: r.Client(),
		HTTPHeader: http.Header{"Authorization": {"Bearer x"}},
	})
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func refusedWith(t *testing.T, resp *http.Response, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("dial succeeded, want HTTP %d", want)
	}
	if resp == nil || resp.StatusCode != want {
		t.Fatalf("dial = %v (response %v), want HTTP %d", err, resp, want)
	}
}

// send writes one binary frame at from and reads it at to.
func send(t *testing.T, ctx context.Context, from, to *websocket.Conn, msg string) {
	t.Helper()
	if err := from.Write(ctx, websocket.MessageBinary, []byte(msg)); err != nil {
		t.Fatalf("write %q: %v", msg, err)
	}
	typ, got, err := to.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(got) != msg {
		t.Fatalf("read = %q, %v, %v; want binary %q", got, typ, err, msg)
	}
}

// A remote that reaches the relay while the local's upgrade is still being
// recorded is paired, not refused: the local end is waiting from its admission.
func TestRemoteDuringLocalRegistrationIsPaired(t *testing.T) {
	r := New(t)
	r.HoldLocalUntilRemote()
	ctx := testCtx(t)

	local, resp, err := dial(ctx, r, "s1", "local")
	if err != nil {
		t.Fatalf("local dial: %v (response %v)", err, resp)
	}
	remote, resp, err := dial(ctx, r, "s1", "remote")
	if err != nil {
		t.Fatalf("remote dial: %v (response %v)", err, resp)
	}
	send(t, ctx, remote, local, "hello")
	send(t, ctx, local, remote, "hi")
}

// The contract's refusals still hold with a reservation in play.
func TestRelayRefusesOutsideTheContract(t *testing.T) {
	r := New(t)
	ctx := testCtx(t)

	_, resp, err := dial(ctx, r, "s1", "remote")
	refusedWith(t, resp, err, http.StatusNotFound) // no local end at all

	r.HoldLocalUntilRemote()
	local, resp, err := dial(ctx, r, "s1", "local")
	if err != nil {
		t.Fatalf("local dial: %v (response %v)", err, resp)
	}
	// The first local is held, not yet recorded: a second local is still refused.
	_, resp, err = dial(ctx, r, "s1", "local")
	refusedWith(t, resp, err, http.StatusNotFound)

	remote, resp, err := dial(ctx, r, "s1", "remote")
	if err != nil {
		t.Fatalf("remote dial: %v (response %v)", err, resp)
	}
	send(t, ctx, remote, local, "ping")

	_, resp, err = dial(ctx, r, "s1", "remote") // second remote on a paired session
	refusedWith(t, resp, err, http.StatusNotFound)
	_, resp, err = dial(ctx, r, "s1", "other") // unknown role
	refusedWith(t, resp, err, http.StatusNotFound)
}
