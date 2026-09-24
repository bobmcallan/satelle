package agentstep

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// usageNote turns an adapter's result into the explicit-unavailable evidence
// recorded beside a row's token numbers (sty_c8d45201).
func TestUsageNote(t *testing.T) {
	// Measured with a provider-reported split: no note fields.
	n := usageNote(agentcli.UsageResult{Available: true, CacheSplitAvailable: true})
	if n.UsageUnavailableReason != "" || n.CacheSplitUnavailable {
		t.Errorf("measured+split note = %+v, want empty", n)
	}
	// Measured but the provider named no cache fields: split unavailable.
	n = usageNote(agentcli.UsageResult{Available: true})
	if n.UsageUnavailableReason != "" || !n.CacheSplitUnavailable {
		t.Errorf("no-split note = %+v", n)
	}
	// Unavailable: the adapter's own reason is kept, and an unnamed one still
	// gets a reason — never a bare false.
	n = usageNote(agentcli.UsageResult{UnavailableReason: "grok adapter: no usage"})
	if n.UsageUnavailableReason != "grok adapter: no usage" || !n.CacheSplitUnavailable {
		t.Errorf("unavailable note = %+v", n)
	}
	if n = usageNote(agentcli.UsageResult{}); n.UsageUnavailableReason == "" {
		t.Error("unavailable usage with no adapter reason must still record a reason")
	}
}

func TestAddUsageNote(t *testing.T) {
	row := map[string]any{}
	addUsageNote(row, agentcli.UsageResult{UnavailableReason: "acp adapter: session/prompt response carried no usage"})
	if r, _ := row["usage_unavailable_reason"].(string); !strings.HasPrefix(r, "acp adapter:") {
		t.Errorf("row = %+v", row)
	}
	if row["cache_split_unavailable"] != true {
		t.Errorf("row = %+v, want cache_split_unavailable", row)
	}
	row = map[string]any{}
	addUsageNote(row, agentcli.UsageResult{Available: true, CacheSplitAvailable: true})
	if len(row) != 0 {
		t.Errorf("measured row grew fields: %+v", row)
	}
}
