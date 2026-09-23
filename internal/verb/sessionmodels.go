package verb

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
)

// Session-model roles (sty_7069bced / epic:model-selection order:3) — the
// three sessions the model-selection resolver's inherited/creator tiers read.
const (
	SessionModelRoleOrchestrator = "orchestrator"
	SessionModelRoleInLoop       = "in-loop"
	SessionModelRoleCreator      = "creator"
)

// sessionModelRow is the session_model ledger row's payload shape.
type sessionModelRow struct {
	Role       string `json:"role"`
	Model      string `json:"model"`
	Executable string `json:"executable,omitempty"`
}

// RecordSessionModel appends a session_model ledger row for itemID's role —
// the capture half of the model-selection resolver's inherited/creator tiers
// (sty_7069bced). model is "unknown" when the harness reported none; empty is
// normalised to "unknown" so a row always names a definite state. Best-effort:
// nil-safe when no ledger is wired.
func RecordSessionModel(ctx context.Context, itemID, actor, role, model, executable string) {
	if ledgerStore == nil || strings.TrimSpace(itemID) == "" || strings.TrimSpace(role) == "" {
		return
	}
	m := strings.TrimSpace(model)
	if m == "" {
		m = "unknown"
	}
	payload, err := json.Marshal(sessionModelRow{Role: role, Model: m, Executable: strings.TrimSpace(executable)})
	if err != nil {
		return
	}
	_, _ = ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: itemID, Kind: ledger.KindSessionModel, Actor: actor, Payload: payload,
	}, time.Now())
}

// recordCreatorSessionModel captures the story-creating session's model
// (sty_7069bced) — config.SelectModel's creator tier reads the latest
// session_model{creator} row for a story. There is no separate "creator"
// publish: the creating session IS the in-loop session at creation time, so
// this reads its already-published in-loop model and re-records it under the
// creator role for THIS story. Best-effort: an unresolved session or model
// records "unknown" via RecordSessionModel rather than skipping the write.
func recordCreatorSessionModel(ctx context.Context, itemID string) {
	sid := config.ResolveSession()
	model, exe := config.ResolveSessionModel(sid, SessionModelRoleInLoop)
	RecordSessionModel(ctx, itemID, "", SessionModelRoleCreator, model, exe)
}

// recordEngageSessionModel captures the engaging session's model for itemID
// (sty_7069bced) — config.SelectModel's inherited in-loop tier. Called on
// every engaging transition, not just the first: a later engage (e.g.
// blocked → in_progress) may be driven by a different session than before.
func recordEngageSessionModel(ctx context.Context, itemID string) {
	sid := config.ResolveSession()
	model, exe := config.ResolveSessionModel(sid, SessionModelRoleInLoop)
	RecordSessionModel(ctx, itemID, "", SessionModelRoleInLoop, model, exe)
}

// SessionModels resolves the latest published model per role — orchestrator,
// in-loop, creator — for itemID from the evidence ledger. It is the resolver
// agentstep.Engine.SetSessionModelsResolver wires so config.SelectModel's
// inherited/creator tiers have something to read (sty_7069bced). Nil-safe: no
// ledger wired, or no rows recorded, yields the zero (unknown) SessionModel
// for every role, and SelectModel falls through cleanly to the next tier.
func SessionModels(ctx context.Context, itemID string) (orchestrator, inLoop, creator config.SessionModel) {
	if ledgerStore == nil || strings.TrimSpace(itemID) == "" {
		return
	}
	entries, err := ledgerStore.ListByStory(ctx, itemID, ledger.KindSessionModel)
	if err != nil {
		return
	}
	// ListByStory returns oldest first; keep overwriting so the LAST row per
	// role — the most recent capture — wins.
	for _, e := range entries {
		var row sessionModelRow
		if jerr := json.Unmarshal(e.Payload, &row); jerr != nil {
			continue
		}
		sm := config.SessionModel{Model: row.Model, Executable: row.Executable}
		switch row.Role {
		case SessionModelRoleOrchestrator:
			orchestrator = sm
		case SessionModelRoleInLoop:
			inLoop = sm
		case SessionModelRoleCreator:
			creator = sm
		}
	}
	return orchestrator, inLoop, creator
}
