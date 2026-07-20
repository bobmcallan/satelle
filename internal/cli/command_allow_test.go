package cli

import "testing"

func TestCommandAllowDenyUnconfigured(t *testing.T) {
	if _, deny := commandAllowDenyWith(nil, []string{"push"}, "in_progress"); deny {
		t.Fatal("nil policy must not deny")
	}
	if _, deny := commandAllowDenyWith(map[string][]string{}, []string{"push"}, "in_progress"); deny {
		t.Fatal("empty policy must not deny")
	}
}

func TestCommandAllowDenyBlocksPushAtInProgress(t *testing.T) {
	policy := map[string][]string{"push": {"release"}}
	reason, deny := commandAllowDenyWith(policy, []string{"push"}, "in_progress")
	if !deny {
		t.Fatal("want deny for push at in_progress")
	}
	if reason == "" || !containsStr(reason, "release") {
		t.Fatalf("reason should name allowed states, got %q", reason)
	}
}

func TestCommandAllowDenyAllowsPushAtRelease(t *testing.T) {
	policy := map[string][]string{"push": {"release"}}
	if _, deny := commandAllowDenyWith(policy, []string{"push"}, "release"); deny {
		t.Fatal("push at release must allow")
	}
}

func TestCommandAllowDenyIgnoresUnlistedSubcommand(t *testing.T) {
	policy := map[string][]string{"push": {"release"}}
	// commit not listed → no step restriction (engage gate still applies elsewhere)
	if _, deny := commandAllowDenyWith(policy, []string{"commit"}, "in_progress"); deny {
		t.Fatal("unlisted subcommand must not be step-restricted")
	}
}

func TestCommandAllowRestricts(t *testing.T) {
	if commandAllowRestrictsWith(nil, []string{"push"}) {
		t.Fatal("nil policy")
	}
	policy := map[string][]string{"push": {"release"}}
	if !commandAllowRestrictsWith(policy, []string{"status", "push"}) {
		t.Fatal("want restricts when push present")
	}
	if commandAllowRestrictsWith(policy, []string{"status"}) {
		t.Fatal("status alone not restricted")
	}
}

func TestGitSubcommands(t *testing.T) {
	got := gitSubcommands("git push origin main")
	if len(got) != 1 || got[0] != "push" {
		t.Fatalf("got %v", got)
	}
	got = gitSubcommands(`echo "git push" && git commit -m x`)
	if len(got) != 1 || got[0] != "commit" {
		// quoted prose must not count; only real commit segment
		t.Fatalf("got %v want [commit]", got)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
