package web

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// heartbeatInterval keeps an idle SSE stream alive (and detects a dead peer via
// the write error) so a connection — and the realtime it carries — survives idle
// gaps through proxies/WSL rather than silently dying between CLI updates.
const heartbeatInterval = 25 * time.Second

// hub is the realtime doorbell — the satellites SSE model, stripped of auth.
// It fans a topic string out to every connected /events client; the page then
// re-fetches the affected panel over a normal request. Messages carry only a
// topic ("stories"/"tasks"/"docs"), never row data.
type hub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newHub() *hub {
	return &hub{clients: make(map[chan string]struct{})}
}

// publish delivers topic to every subscriber, dropping the message for any slow
// client rather than blocking (a missed doorbell self-heals on the next one).
func (h *hub) publish(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- topic:
		default: // slow client — skip; it refetches on the next trigger
		}
	}
}

func (h *hub) subscribe() chan string {
	ch := make(chan string, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// serveEvents is the GET /events SSE handler. It streams "trigger" events whose
// data is the topic, until the client disconnects. No auth (local tier).
func (h *hub) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.subscribe()
	defer h.unsubscribe(ch)

	// An initial comment opens the stream so the browser's EventSource fires
	// onopen immediately.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	beat := time.NewTicker(heartbeatInterval)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			// Keepalive comment. A write error means the peer is gone — return so
			// the client's EventSource reconnects (and reconciles on open).
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case topic, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: trigger\ndata: %s\n\n", topic); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
