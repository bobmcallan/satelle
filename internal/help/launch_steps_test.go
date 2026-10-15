package help

import (
	"strings"
	"testing"
)

// The launch-steps topic carries an Azure and an AWS example written only as
// configuration, and states what satelle does and does not do (sty_b8310408).
func TestGlobalAgentsTopic_LaunchSteps(t *testing.T) {
	topic, ok := Get("global-agents")
	if !ok {
		t.Fatal("global-agents topic missing")
	}
	flat := strings.Join(strings.Fields(topic.Body), " ")
	for _, want := range []string{
		"[profiles.ops-az.launch]", "[[profiles.ops-az.launch.steps]]",
		"[profiles.ops-aws.launch]", "[[profiles.ops-aws.launch.steps]]",
		"${SATELLE_PROFILE_DIR}/az", "${SATELLE_PROFILE_DIR}/aws/config",
		"satelle runs what the profile declares",
		"does not enforce rights or isolate secrets",
		"The cloud's roles and policy, plus a separate OS user for the agent, do the enforcing",
		"A tool a step runs may persist credentials in its own cache",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("global-agents topic lacks %q", want)
		}
	}
}

// No topic says the launcher runs as a named cloud identity: it runs the steps a
// profile declares and knows no provider.
func TestNoTopicClaimsALauncherIdentity(t *testing.T) {
	for _, topic := range List() {
		flat := strings.Join(strings.Fields(topic.Body), " ")
		for _, banned := range []string{"as the Azure or AWS identity", "Cloud identity", "cloud identity"} {
			if strings.Contains(flat, banned) {
				t.Errorf("topic %s says %q", topic.Name, banned)
			}
		}
	}
	dispatch, ok := Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch topic missing")
	}
	flat := strings.Join(strings.Fields(dispatch.Body), " ")
	if !strings.Contains(flat, "runs the launch steps its in-loop agent profile declares (see `satelle help global-agents`)") {
		t.Error("agent-dispatch does not point at satelle help global-agents for launch steps")
	}
}
