// Package relaytest is the in-process stand-in for the satelle-server relay: a
// TLS WebSocket server on loopback that pairs the two ends of a remotechan
// session by session id and forwards every binary message unchanged. It
// implements the "Relay contract" documented in package remotechan, taps every
// frame it forwards so a test can read what a real relay could read, and can be
// scripted to reject bearers with a 401. It is test support only.
package relaytest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// Frame is one message the relay forwarded, with who sent it ("local" or "remote").
type Frame struct {
	From string
	Data []byte
}

type slot struct{ local, remote *websocket.Conn }

// Relay is the stand-in relay. Create it with New.
type Relay struct {
	srv *httptest.Server

	mu       sync.Mutex
	slots    map[string]*slot
	conns    []*websocket.Conn
	frames   []Frame
	bearers  []string
	attempts int
	upgrades int
	reject   int
	other    http.Handler
}

// New starts a relay for the life of t.
func New(t testing.TB) *Relay {
	t.Helper()
	r := &Relay{slots: map[string]*slot{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(func() {
		r.mu.Lock()
		conns := append([]*websocket.Conn(nil), r.conns...)
		r.mu.Unlock()
		for _, c := range conns {
			c.CloseNow()
		}
		r.srv.Close()
	})
	return r
}

// URL is the relay base URL to hand to remotechan (https; the dialer upgrades it).
func (r *Relay) URL() string { return r.srv.URL + "/relay" }

// Client is an HTTP client that trusts the relay's test certificate.
func (r *Relay) Client() *http.Client { return r.srv.Client() }

// RejectBearers makes the next n local upgrades fail with 401 whatever bearer
// they carry, so a test can exercise the refresh-and-retry.
func (r *Relay) RejectBearers(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reject = n
}

// Attempts counts every upgrade request the relay received, rejected or not.
func (r *Relay) Attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

// Upgrades counts the connections the relay accepted.
func (r *Relay) Upgrades() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.upgrades
}

// Bearers lists the bearer each local upgrade request carried, in order.
func (r *Relay) Bearers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bearers...)
}

// Frames is a copy of every message the relay forwarded, in arrival order.
func (r *Relay) Frames() []Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Frame, len(r.frames))
	for i, f := range r.frames {
		out[i] = Frame{From: f.From, Data: append([]byte(nil), f.Data...)}
	}
	return out
}

// Mount serves every path outside /relay/ with h, so a test can put a stand-in for
// the server's other routes on the host the relay is under.
func (r *Relay) Mount(h http.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.other = h
}

func (r *Relay) serve(w http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, "/relay/") {
		r.mu.Lock()
		other := r.other
		r.mu.Unlock()
		if other != nil {
			other.ServeHTTP(w, req)
			return
		}
	}
	session := strings.TrimPrefix(req.URL.Path, "/relay/")
	role := req.URL.Query().Get("role")

	r.mu.Lock()
	r.attempts++
	if role == "local" {
		bearer := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		r.bearers = append(r.bearers, bearer)
		if r.reject > 0 || bearer == "" {
			if r.reject > 0 {
				r.reject--
			}
			r.mu.Unlock()
			http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
			return
		}
	}
	sl := r.slots[session]
	switch {
	case role == "local" && sl != nil && sl.local != nil,
		role == "remote" && (sl == nil || sl.local == nil || sl.remote != nil),
		role != "local" && role != "remote":
		r.mu.Unlock()
		http.Error(w, "no such pairing", http.StatusNotFound)
		return
	}
	r.mu.Unlock()

	c, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)

	r.mu.Lock()
	r.upgrades++
	r.conns = append(r.conns, c)
	if r.slots[session] == nil {
		r.slots[session] = &slot{}
	}
	sl = r.slots[session]
	if role == "local" {
		sl.local = c
	} else {
		sl.remote = c
	}
	r.mu.Unlock()

	r.pump(req.Context(), c, sl, role)

	r.mu.Lock()
	peer := sl.remote
	if role == "remote" {
		peer = sl.local
		sl.remote = nil
	} else {
		sl.local = nil
	}
	r.mu.Unlock()
	c.CloseNow()
	if peer != nil {
		peer.CloseNow()
	}
}

// pump reads c until it ends, tapping each message and forwarding it to the
// other end if that end is present.
func (r *Relay) pump(ctx context.Context, c *websocket.Conn, sl *slot, role string) {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		r.mu.Lock()
		r.frames = append(r.frames, Frame{From: role, Data: append([]byte(nil), data...)})
		peer := sl.remote
		if role == "remote" {
			peer = sl.local
		}
		r.mu.Unlock()
		if peer != nil {
			_ = peer.Write(ctx, typ, data)
		}
	}
}
