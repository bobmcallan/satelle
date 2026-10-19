package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

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
