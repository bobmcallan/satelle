// Package push is the CLI→local-UI-server change publisher (epic:serve-split).
// When satelle.toml configures [server] endpoint, mutating verbs emit a
// fire-and-forget change event to the server ingest. Unset = inert (no network).
// Failures never alter verb outcomes or block the caller (sty_126228b2).
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout bounds a single POST. Short so a black-holed endpoint cannot
// hang a process; the call is already asynchronous from the verb path.
const DefaultTimeout = 500 * time.Millisecond

// Event is the JSON body POSTed to the configured server's change ingest.
type Event struct {
	RepoKey string `json:"repo_key"`
	Topic   string `json:"topic"`  // coarse panel: stories | tasks | docs
	Entity  string `json:"entity"` // story | task | doc | ledger (derived)
	At      string `json:"at"`     // RFC3339 UTC
}

// Publisher posts change events to Endpoint. Safe for concurrent use.
// A zero Publisher (empty Endpoint) is a no-op.
type Publisher struct {
	Endpoint string
	RepoKey  string
	// Client is optional; nil uses a client with DefaultTimeout.
	Client *http.Client
	// Path is the ingest path under Endpoint; default "/ingest/change".
	Path string
	// Now is optional; defaults to time.Now.
	Now func() time.Time
	// OnPost is an optional test hook invoked just before the request is sent
	// (after body marshal). Not used in production.
	OnPost func(*http.Request)
}

// Active reports whether the publisher will attempt network I/O.
func (p *Publisher) Active() bool {
	return p != nil && strings.TrimSpace(p.Endpoint) != ""
}

// Notify is the ChangeNotifier sink: fire-and-forget POST. Never blocks the
// caller beyond the cost of scheduling a goroutine. Never panics.
func (p *Publisher) Notify(topic string) {
	if !p.Active() {
		return
	}
	go p.post(topic)
}

func (p *Publisher) post(topic string) {
	defer func() { _ = recover() }()
	endpoint := strings.TrimRight(strings.TrimSpace(p.Endpoint), "/")
	path := p.Path
	if path == "" {
		path = "/ingest/change"
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	ev := Event{
		RepoKey: p.RepoKey,
		Topic:   topic,
		Entity:  entityForTopic(topic),
		At:      now().UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if p.OnPost != nil {
		p.OnPost(req)
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func entityForTopic(topic string) string {
	switch topic {
	case "stories":
		return "story"
	case "tasks":
		return "task"
	case "docs":
		return "doc"
	default:
		return topic
	}
}
