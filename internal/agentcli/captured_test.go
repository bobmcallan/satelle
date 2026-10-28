package agentcli

import (
	"strings"
	"testing"
)

// wantCapturedContracts are the contracts a cursor drift report must name. They
// are written out here, not read from CapturedVersions, so a contract dropped
// from the table fails this test instead of passing it circularly.
var wantCapturedContracts = []string{
	"stop cap", "hook timeout handling", "envelope shapes", "ACP methods", "usage fields",
}

func TestCapturedCursorContractsEachCarryBasisAndFixture(t *testing.T) {
	b, ok := CapturedFor(HarnessCursor)
	if !ok {
		t.Fatal("no cursor capture recorded")
	}
	if len(b.Contracts) != len(wantCapturedContracts) {
		t.Fatalf("contracts = %d, want %d", len(b.Contracts), len(wantCapturedContracts))
	}
	for i, name := range wantCapturedContracts {
		c := b.Contracts[i]
		if c.Name != name || strings.TrimSpace(c.Basis) == "" || strings.TrimSpace(c.Fixture) == "" {
			t.Errorf("contract %d = %+v, want name %q with a basis and a fixture", i, c, name)
		}
	}
}

func TestSessionDriftLine(t *testing.T) {
	captured, _ := CapturedVersion(HarnessCursor)
	cases := map[string]struct {
		harness, payload string
		want             bool
	}{
		"different cursor build": {HarnessCursor, `{"hook_event_name":"sessionStart","cursor_version":"2026.10.22-27dcf5f"}`, true},
		"matching build":         {HarnessCursor, `{"hook_event_name":"sessionStart","cursor_version":"` + captured + `"}`, false},
		"no version in payload":  {HarnessCursor, `{"hook_event_name":"sessionStart"}`, false},
		"claude has no capture":  {HarnessClaude, `{"hook_event_name":"SessionStart","cursor_version":"9"}`, false},
		"unparseable payload":    {HarnessCursor, `not json`, false},
	}
	for name, tc := range cases {
		got := SessionDriftLine(tc.harness, []byte(tc.payload))
		if (got != "") != tc.want {
			t.Errorf("%s: line = %q, want line=%v", name, got, tc.want)
		}
		if !tc.want {
			continue
		}
		for _, w := range append([]string{"2026.10.22-27dcf5f", captured, "satelle agent recheck cursor"}, wantCapturedContracts...) {
			if !strings.Contains(got, w) {
				t.Errorf("%s: line missing %q:\n%s", name, w, got)
			}
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"2026.10.22-27dcf5f\n": "2026.10.22-27dcf5f",
		"\n  1.2.3  \nextra\n": "1.2.3",
		"":                     "",
		" \n\t\n":              "",
	} {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStopCapBasisReadsFromCapturedTable(t *testing.T) {
	basis := stopResumes[HarnessCursor].Basis
	v, _ := CapturedVersion(HarnessCursor)
	if !strings.Contains(basis, v) || !strings.Contains(basis, capturedBasis(HarnessCursor, ContractStopCap)) {
		t.Errorf("cursor stop-cap basis does not come from CapturedVersions: %s", basis)
	}
}
