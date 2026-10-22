package agentstep

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// sty_23ceeca2 AC1: a cloud-placed step records the skill it delivered, by the
// hash of the body held in the index (the frontmatter is dropped from the prompt,
// never from the hash).
func TestCloudDispatchRecordsDeliveredSkills(t *testing.T) {
	f := newCloudFix(t)
	c := &fakeCloud{}
	c.act = func(branch, nonce string) {
		f.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "from cloud\n"})
	}
	installCloud(t, agentcli.HarnessClaude, c)
	ce := newCloudEngine(t, f, cloudDocs(cloudStepSkill), cloudBinding("claude -p {system}", cloudDocName, cloudTestLongLimit))
	res, err := ce.dispatch(t)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(cloudStepSkill))
	if len(res.Skills) != 1 || res.Skills[0].Name != "cloud-step" || !res.Skills[0].Delivered || res.Skills[0].Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("Skills = %+v, want cloud-step delivered with the sha256 of its body", res.Skills)
	}
}

// AC2: a cloud step whose skill no longer resolves is refused before any session
// is launched under skill_evidence = reject, and launched with the gap recorded
// under the default.
func TestCloudDispatchUndeliveredSkillFollowsConfiguration(t *testing.T) {
	for _, mode := range []string{"reject", ""} {
		f := newCloudFix(t)
		c := &fakeCloud{}
		c.act = func(branch, nonce string) {
			f.session(t, branch, tipMessage(cloudEvidenceBody, nonce), map[string]string{"b.txt": "from cloud\n"})
		}
		installCloud(t, agentcli.HarnessClaude, c)
		docs := cloudDocs(cloudStepSkill)
		docs.extraSkills = docs.extraSkills[1:] // drop cloud-step, keep the cloud-performer skill
		if mode != "" {
			docs.workflow = withSkillEvidence(docs.workflow, mode)
		}
		ce := newCloudEngine(t, f, docs, cloudBinding("claude -p {system}", cloudDocName, cloudTestLongLimit))
		res, err := ce.dispatch(t)
		if len(res.Skills) != 1 || res.Skills[0].Name != "cloud-step" || res.Skills[0].Delivered {
			t.Fatalf("mode %q: Skills = %+v, want cloud-step undelivered", mode, res.Skills)
		}
		if mode == "reject" {
			if err == nil || !strings.Contains(err.Error(), "cloud-step") || c.calls != 0 {
				t.Errorf("reject: err=%v launches=%d, want a refusal naming the skill and no launch", err, c.calls)
			}
			continue
		}
		if err != nil || c.calls != 1 || res.Evidence == nil || len(res.Evidence.Undelivered) != 1 {
			t.Errorf("record: err=%v launches=%d evidence=%+v, want a launch with the gap recorded", err, c.calls, res.Evidence)
		}
	}
}
