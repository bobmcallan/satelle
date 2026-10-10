package agentcli

import (
	"encoding/json"
	"strings"
)

// Session identity of a cursor-agent conversation (sty_2439f4fd). cursor exports
// the conversation id to its Shell children as CURSOR_CONVERSATION_ID and carries
// the same value in its hook payloads as conversation_id (and session_id)
// (testdata/cursor/21-shell-env.txt, 21-stop.log). A gate started from a Shell
// call is stamped with it, and the stop hook of the same conversation reads it
// back, so the two sides name one session. Its hook processes do not see the env
// name (6-hook-env-names.txt): the payload is their only source.
const cursorConversationIDEnv = "CURSOR_CONVERSATION_ID"

// sessionEnvKeys are the environment variables an adapter publishes its session
// id in, in the order they are asked. A harness absent here has none, which
// SessionIDFromEnv reports as no answer — nothing unrecognised is assumed to be
// cursor or claude.
var sessionEnvKeys = []string{cursorConversationIDEnv}

// SessionIDFromEnv returns the session id an adapter publishes in environ
// (KEY=VALUE entries), "" when none does. Only the cursor adapter answers.
func SessionIDFromEnv(environ []string) string {
	for _, want := range sessionEnvKeys {
		for _, e := range environ {
			key, val, _ := strings.Cut(e, "=")
			if key != want {
				continue
			}
			if id := strings.TrimSpace(val); id != "" {
				return id
			}
		}
	}
	return ""
}

// HookSessionID returns the session id a hook payload carries in an adapter's own
// shape, "" when no adapter claims the payload (the caller then reads the shared
// session_id/sessionId fields). Only cursor answers: conversation_id, falling back
// to session_id on a payload that names itself cursor's through cursor_version.
func HookSessionID(raw []byte) string {
	var ev struct {
		ConversationID string `json:"conversation_id"`
		SessionID      string `json:"session_id"`
		CursorVersion  string `json:"cursor_version"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return ""
	}
	if id := strings.TrimSpace(ev.ConversationID); id != "" {
		return id
	}
	if strings.TrimSpace(ev.CursorVersion) != "" {
		return strings.TrimSpace(ev.SessionID)
	}
	return ""
}
