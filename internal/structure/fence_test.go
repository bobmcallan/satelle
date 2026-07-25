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
