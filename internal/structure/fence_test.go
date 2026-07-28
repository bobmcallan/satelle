package structure

import "testing"

func TestCheckFence(t *testing.T) {
	body := "---\nname: x\n---\n# Gate\n\n```check\n#!/bin/sh\necho hello\n```\n\ntrailing\n"
	got := CheckFence(body)
	if got != "#!/bin/sh\necho hello" {
		t.Fatalf("got %q", got)
	}
	if CheckFence("no fence") != "" {
		t.Fatal("want empty when no fence")
	}
	// info string with language suffix
	body2 := "```check bash\nexit 0\n```\n"
	if CheckFence(body2) != "exit 0" {
		t.Fatalf("got %q", CheckFence(body2))
	}
}

func TestIsCodedCheck(t *testing.T) {
	if !IsCodedCheck("---\nname: x\n---\n```check\nexit 0\n```\n") {
		t.Error("fenced check must be coded")
	}
	if got := CheckCommand("---\nname: x\ncheck: \"true\"\n---\nbody\n"); got != "true" {
		t.Errorf("frontmatter check: got %q", got)
	}
	if IsCodedCheck("---\nname: x\n---\nReturn JSON decision notes.\n") {
		t.Error("LLM rubric must not be coded")
	}
	if CheckCommand("---\nname: x\n---\n```check\nexit 0\n```\n") != "exit 0" {
		t.Error("CheckCommand must prefer fence")
	}
}
