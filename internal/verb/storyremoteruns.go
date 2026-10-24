package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// remoteExecutionsSection renders the story's execution_link rows, in time order:
// each execution a remote node ran about the story, with its task, the remote and
// the outcome it concluded with. Empty for a story no node has run.
func remoteExecutionsSection(ctx context.Context, item workitem.Item) string {
	l, err := requireLedger()
	if err != nil {
		return ""
	}
	rows, err := l.ListByStory(ctx, item.ID, ledger.KindExecutionLink)
	if err != nil || len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Remote executions\n\n")
	for _, e := range rows {
		var p ledger.ExecutionLinkPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		fmt.Fprintf(&b, "- %s of %s on remote %s · %s\n", p.Execution, p.Task, p.Remote, p.Outcome)
	}
	return b.String()
}

// remoteRunsSection renders the story's remote_run rows (sty_2f3baf0a), in time
// order, appended to the route view: where and as whom each remote step ran, and
// the run id its process was given. Empty for a story no remote has run, so such a
// route reads exactly as before.
func remoteRunsSection(ctx context.Context, item workitem.Item) string {
	l, err := requireLedger()
	if err != nil {
		return ""
	}
	rows, err := l.ListByStory(ctx, item.ID, ledger.KindRemoteRun)
	if err != nil || len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Remote runs\n\n")
	for _, e := range rows {
		var p ledger.RemoteRunPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		host := "host (unnamed)"
		if p.Host != "" {
			host = "host " + p.Host
		}
		fmt.Fprintf(&b, "- %s on remote %s @ satelle %s · %s · as %s · %s → %s · run %s", p.Step, p.Remote, p.Version, host, p.Label, p.Started, p.Ended, p.RunID)
		if p.Error != "" {
			fmt.Fprintf(&b, " · failed: %s", p.Error)
		}
		b.WriteString("\n")
	}
	return b.String()
}
