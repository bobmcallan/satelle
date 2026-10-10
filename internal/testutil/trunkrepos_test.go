package testutil

import "testing"

// The fixture's remote refuses a force push, so a mechanism that forced would
// fail its own test rather than pass quietly.
func TestTrunkRepos_DeniesForce(t *testing.T) {
	r := NewTrunkRepos(t)
	r.PublishFromPusher(t, "a.txt")
	before := r.RemoteHead(t)
	r.Git(t, r.Pusher, "commit", "--quiet", "--amend", "-m", "rewritten")
	if out, err := r.GitErr(r.Pusher, "push", "--force", "origin", "main"); err == nil {
		t.Fatalf("the remote accepted a force push:\n%s", out)
	}
	if got := r.RemoteHead(t); got != before {
		t.Errorf("remote head moved to %s despite the refused force push", got)
	}
}
