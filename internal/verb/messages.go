package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-message",
		Description: "Append a directed agent message to a story",
		Invoke:      storyMessage,
	})
	Register(&Verb{
		Name:        "story-messages",
		Description: "List directed agent messages on a story (read-only)",
		Invoke:      storyMessages,
	})
}

// AgentMessage is the stored/returned shape of one agent_message row
// (decision-agent-messaging.md). Story id and created_at live on the ledger
// row; the JSON payload carries from/to/body/engagement_sha.
type AgentMessage struct {
	ID            string    `json:"id,omitempty"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	EngagementSHA string    `json:"engagement_sha"`
}

type storyMessageReq struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	Body string `json:"body"`
}

type storyMessagesReq struct {
	ID    string `json:"id"`
	Since string `json:"since,omitempty"`
	To    string `json:"to,omitempty"`
}

func storyMessage(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req storyMessageReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.ID)
	from := strings.TrimSpace(req.From)
	body := strings.TrimSpace(req.Body)
	if id == "" {
		return nil, fmt.Errorf("story-message: id required")
	}
	if from == "" {
		return nil, fmt.Errorf("story-message: from required")
	}
	if body == "" {
		return nil, fmt.Errorf("story-message: body required")
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		to = "*"
	}
	it, err := store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.Kind != workitem.KindStory {
		return nil, fmt.Errorf("story-message: %s is not a story", id)
	}
	engagementSHA := ""
	if sha, _, ok := currentEngagementWindow(ctx, id); ok {
		engagementSHA = sha
	}
	payload, err := json.Marshal(AgentMessage{
		From:          from,
		To:            to,
		Body:          body,
		EngagementSHA: engagementSHA,
	})
	if err != nil {
		return nil, err
	}
	e, err := ls.Append(ctx, ledger.AppendInput{
		StoryID: id,
		Kind:    ledger.KindAgentMessage,
		Actor:   from,
		Body:    body,
		Payload: payload,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	out := rowToAgentMessage(e)
	return json.Marshal(out)
}

func storyMessages(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req storyMessagesReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, fmt.Errorf("story-messages: id required")
	}
	it, err := store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.Kind != workitem.KindStory {
		return nil, fmt.Errorf("story-messages: %s is not a story", id)
	}
	var since time.Time
	if s := strings.TrimSpace(req.Since); s != "" {
		t, perr := time.Parse(time.RFC3339, s)
		if perr != nil {
			return nil, fmt.Errorf("story-messages: --since must be RFC3339: %w", perr)
		}
		since = t
	}
	to := strings.TrimSpace(req.To)
	msgs, err := listAgentMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]AgentMessage, 0, len(msgs))
	for _, m := range msgs {
		if !since.IsZero() && m.CreatedAt.Before(since) {
			continue
		}
		if to != "" && m.To != to && m.To != "*" {
			continue
		}
		out = append(out, m)
	}
	return json.Marshal(out)
}

func listAgentMessages(ctx context.Context, storyID string) ([]AgentMessage, error) {
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindAgentMessage)
	if err != nil {
		return nil, err
	}
	out := make([]AgentMessage, 0, len(entries))
	for _, e := range entries {
		out = append(out, rowToAgentMessage(e))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func rowToAgentMessage(e ledger.Entry) AgentMessage {
	m := AgentMessage{ID: e.ID, CreatedAt: e.CreatedAt, Body: e.Body}
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &m)
		m.ID = e.ID
		m.CreatedAt = e.CreatedAt
	}
	return m
}

// MessagesSince returns engagement-windowed agent messages whose `to` is in
// addresses or is "*". Nil on any read error or missing baseline — never fails
// a transition. Oldest first. The engine owns budget truncation.
func MessagesSince(ctx context.Context, itemID string, addresses []string) []AgentMessage {
	if strings.TrimSpace(itemID) == "" {
		return nil
	}
	windowSHA, windowAt, ok := currentEngagementWindow(ctx, itemID)
	if !ok {
		return nil
	}
	want := map[string]bool{"*": true}
	for _, a := range addresses {
		if a = strings.TrimSpace(a); a != "" {
			want[a] = true
		}
	}
	msgs, err := listAgentMessages(ctx, itemID)
	if err != nil {
		return nil
	}
	out := make([]AgentMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.CreatedAt.Before(windowAt) {
			continue
		}
		if m.EngagementSHA != "" && windowSHA != "" && m.EngagementSHA != windowSHA {
			continue
		}
		if !want[m.To] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// currentEngagementWindow is the SHA and time MessagesSince windows on, and
// the SHA story-message stamps. Resume re-anchor wins over the first
// baseline so write and read cannot diverge (sty_2db624d0).
func currentEngagementWindow(ctx context.Context, storyID string) (sha string, at time.Time, ok bool) {
	p, _, baseAt, err := firstEngagementBaseline(ctx, storyID)
	if err != nil {
		return "", time.Time{}, false
	}
	sha, at = p.HeadSHA, baseAt
	if rsha, rat, rok := latestResumeReanchor(ctx, storyID); rok {
		return rsha, rat, true
	}
	return sha, at, true
}
