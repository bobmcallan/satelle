package ledger

import "testing"

// TestIsToolPermissionRow pins sty_8eae81ac AC4: the current KindToolPermission
// kind is always a permission row; a legacy agent_invocation row is one only
// when it carries the decided_by/decision/tool shape with no usage fields — an
// ordinary invocation (even a zero-cost/zero-usage one) is never mistaken for
// a permission event, and a permission-shaped row under any other kind is not
// swept in either.
func TestIsToolPermissionRow(t *testing.T) {
	cases := []struct {
		name string
		e    Entry
		want bool
	}{
		{
			"current kind, any payload",
			Entry{Kind: KindToolPermission, Payload: []byte(`{}`)},
			true,
		},
		{
			"legacy agent_invocation with the permission shape",
			Entry{Kind: KindAgentInvocation, Payload: []byte(`{"tool":"Edit","kind":"permission","decision":"allow","decided_by":"policy"}`)},
			true,
		},
		{
			"ordinary agent_invocation with usage",
			Entry{Kind: KindAgentInvocation, Payload: []byte(`{"agent":"coder","tokens_total":120,"usage_available":true}`)},
			false,
		},
		{
			"ordinary agent_invocation with an explicit measured zero",
			Entry{Kind: KindAgentInvocation, Payload: []byte(`{"agent":"coder","tokens_total":0,"usage_available":true}`)},
			false,
		},
		{
			"agent_invocation missing one of the three permission fields",
			Entry{Kind: KindAgentInvocation, Payload: []byte(`{"tool":"Edit","decision":"allow"}`)},
			false,
		},
		{
			"empty payload",
			Entry{Kind: KindAgentInvocation, Payload: nil},
			false,
		},
		{
			"a different kind entirely",
			Entry{Kind: KindStatusTransition, Payload: []byte(`{"tool":"Edit","kind":"permission","decision":"allow","decided_by":"policy"}`)},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsToolPermissionRow(c.e); got != c.want {
				t.Errorf("IsToolPermissionRow(%+v) = %v, want %v", c.e, got, c.want)
			}
		})
	}
}
