package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func storyChatCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat <id>",
		Short: "Open a live consultation session on a story",
		Long: `Open a live session (interface=acp or stream) with a named binding and
forward turns. --agent picks the binding (default orchestrator); any other
binding is told it is CONSULTING — its reply is context, not a verdict, and
it does not change status. --from names the speaking role (default
developer-agent under SATELLE_SESSION, else human): both ledger directions
carry it, and the inbox delivers messages addressed to the binding or "*".

Mutator tool requests are denied by satelle when the story is not in an
executor-owned performing state — the PreToolUse edit-gate policy.
command=in-loop is refused rather than opened.

Does not change story status. See satelle help agent-dispatch.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE:        runStoryChat,
	}
	cmd.Flags().String("agent", "", "binding to open the session with (default orchestrator)")
	cmd.Flags().String("from", "", "role the driving side speaks as (default developer-agent under SATELLE_SESSION, else human)")
	cmd.Flags().String("model", "", "model for this session, overriding agents.toml (recorded with source=agent)")
	return cmd
}

// chatFromRole resolves --from: an explicit role wins; otherwise a session id
// means an AGENT is driving (developer-agent), and no session means a human at
// a prompt (sty_a0372443).
func chatFromRole(flag, sessionID string) string {
	if r := strings.TrimSpace(flag); r != "" {
		return r
	}
	if strings.TrimSpace(sessionID) != "" {
		return developerAgentRole
	}
	return chatDefaultFrom
}

const developerAgentRole = "developer-agent"

func runStoryChat(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	eng, a, err := engineForCmd(cmd)
	if err != nil {
		return err
	}
	it, err := a.Store.Stories.Get(cmd.Context(), id)
	if err != nil {
		return err
	}
	if it.Kind != workitem.KindStory {
		return fmt.Errorf("satelle story chat: %s is not a story", id)
	}
	sid := config.ResolveSession()
	if sid != "" {
		config.PublishSession(sid)
		_ = os.Setenv(config.SessionEnv, sid)
	}
	_, _, _ = resolveSeat(true, sid)

	agentFlag, _ := cmd.Flags().GetString("agent")
	fromFlag, _ := cmd.Flags().GetString("from")
	modelFlag, _ := cmd.Flags().GetString("model")
	binding := agentstep.ChatSessionBinding(agentFlag)
	from := chatFromRole(fromFlag, sid)

	// Idle-stall bound for this session's turns (sty_752c4ef2): the binding's
	// own idle_timeout=, else the shared [defaults] table, else the shipped
	// default — same ladder Invoke resolves for a one-shot dispatch. A broken
	// config here must not block chat (advisory: it degrades to the shipped
	// default; the same value is validated hard at `satelle validate`).
	idle := agentstep.DefaultIdleTimeout
	var executable string
	if eff, eerr := requireAgents(a); eerr == nil {
		if bnd, found := eff.Agents.NamedBinding(binding); found {
			if d, ierr := eff.Agents.ResolveIdleTimeout(bnd, agentstep.DefaultIdleTimeout); ierr == nil {
				idle = d
			}
			executable = bnd.ExecutableToken()
		}
	}

	loop := &chatLoop{
		StoryID: it.ID,
		From:    from,
		To:      binding,
		Ledger:  &storeChatLedger{ctx: cmd.Context(), storyID: it.ID, ls: a.Store.Ledger, actor: binding},
		Seat: func() (seatInfo, bool, error) {
			return resolveSeat(true, config.ResolveSession())
		},
		Now:         time.Now,
		IdleTimeout: idle,
	}
	in := cmd.InOrStdin()
	out := cmd.OutOrStdout()
	if stdinIsInteractive(in) {
		br := bufio.NewReader(in)
		loop.Ask = func(req agentcli.PermissionRequest) bool {
			return promptAllow(out, br, req)
		}
	} else {
		loop.Ask = func(agentcli.PermissionRequest) bool { return false }
	}

	role := agentstep.SessionRoleConsult
	if binding == orchestratorRole {
		role = agentstep.SessionRoleDriving
	}
	// The orchestrator session's model (sty_7069bced) — config.SelectModel's
	// inherited tier reads the latest session_model{orchestrator} row. `story
	// chat` IS the orchestrator-driven consultation mechanism regardless of
	// which binding it opens, so every chat session captures under that role.
	baseHandler := loop.EventHandler()
	captureHandler, closeCapture := orchestratorModelCapture(cmd.Context(), it.ID, sid, executable, baseHandler)
	sess, err := eng.OpenSessionAsWithModel(cmd.Context(), binding, role, it, loop.policy(), captureHandler, modelFlag)
	if err != nil {
		return err
	}
	loop.Sess = sess
	defer func() {
		_ = sess.Close()
		closeCapture()
	}()

	fmt.Fprintf(out, "satelle story chat %s  (status %s; %s → %s; /quit to exit)\n", it.ID, it.Status, from, binding)
	if err := loop.Run(cmd.Context(), in, out); err != nil {
		_ = sess.Cancel()
		return err
	}
	return nil
}

// orchestratorModelCapture wraps base so the FIRST EventSessionInit records
// the orchestrator session's model for storyID under
// verb.SessionModelRoleOrchestrator (sty_7069bced) — config.SelectModel's
// inherited-orchestrator tier reads it back via verb.SessionModels.
// EventHandler may be called from concurrent stdout/stderr reader goroutines
// (agentcli.EventHandler's own contract), so capture is guarded and one-shot
// via initCaptured. The returned closeUnrecorded records "unknown" once, if
// no init event ever arrived (the command transport, or a stream transport that never reached
// the init line) — callers run it when the session closes, alongside
// sess.Close.
func orchestratorModelCapture(ctx context.Context, storyID, sid, executable string, base agentcli.EventHandler) (handler agentcli.EventHandler, closeUnrecorded func()) {
	var initCaptured atomic.Bool
	handler = func(ev agentcli.Event) {
		if ev.Kind == agentcli.EventSessionInit && initCaptured.CompareAndSwap(false, true) {
			verb.RecordSessionModel(ctx, storyID, sid, verb.SessionModelRoleOrchestrator, ev.Model, executable)
		}
		if base != nil {
			base(ev)
		}
	}
	closeUnrecorded = func() {
		if !initCaptured.Load() {
			verb.RecordSessionModel(ctx, storyID, sid, verb.SessionModelRoleOrchestrator, "", executable)
		}
	}
	return handler, closeUnrecorded
}

type storeChatLedger struct {
	ctx     context.Context
	storyID string
	ls      *ledger.Store
	// actor is the binding the session was opened with — the agent whose tool
	// boundaries and permission decisions these invocation rows describe. A
	// hardcoded "orchestrator" here would contradict the message rows once the
	// session can be any binding (sty_a0372443).
	actor string
}

func (s *storeChatLedger) WriteMessage(from, to, cc, body string) error {
	req := map[string]any{"id": s.storyID, "from": from, "to": to, "body": body}
	if strings.TrimSpace(cc) != "" {
		req["cc"] = cc
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = verb.Dispatch(s.ctx, "story-message", raw)
	return err
}

func (s *storeChatLedger) WriteInvocation(tool, kind, decision, decidedBy string) error {
	if s.ls == nil {
		return fmt.Errorf("chat ledger: no ledger store")
	}
	payload, err := json.Marshal(map[string]any{
		"tool": tool, "kind": kind, "decision": decision, "decided_by": decidedBy,
	})
	if err != nil {
		return err
	}
	body := fmt.Sprintf("chat %s %s by %s", decision, tool, decidedBy)
	_, err = s.ls.Append(s.ctx, ledger.AppendInput{
		StoryID: s.storyID,
		Kind:    ledger.KindAgentInvocation,
		Actor:   agentstep.ChatSessionBinding(s.actor),
		Body:    body,
		Payload: payload,
	}, time.Now())
	return err
}

func (s *storeChatLedger) ListMessagesSince(since time.Time, to string) ([]verb.AgentMessage, error) {
	req := map[string]any{"id": s.storyID, "to": to}
	if !since.IsZero() {
		req["since"] = since.UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := verb.Dispatch(s.ctx, "story-messages", raw)
	if err != nil {
		return nil, err
	}
	var msgs []verb.AgentMessage
	if err := json.Unmarshal(resp, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}
