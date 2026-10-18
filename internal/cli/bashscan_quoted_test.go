package cli

import "testing"

// A quoted argument to a satelle verb is never a write target, even when its
// $(cat <<'EOF' … EOF) body holds an odd inner quote, a redirect or a mutation verb
// (sty_5fac62b9).
func TestBashQuotedSatelleBodyIsNotAWrite(t *testing.T) {
	const anchor = "/work/repo"
	for _, c := range []string{
		"satelle story message sty_x --body \"$(cat <<'EOF2'\nthe \"x file\nrm internal/x.go\nEOF2\n)\"",
		"satelle story message sty_x --body \"$(cat <<'EOF'\nedit internal/workspace/workspace.go > \"x\"; rm f\nEOF\n)\"",
		"satelle story message sty_x --body \"edit internal/workspace/workspace.go and internal/verb/x.go\"",
	} {
		if bashMutatesTree(c, anchor) {
			t.Errorf("bashMutatesTree(%q) = true, want false", c)
		}
	}
	if !bashMutatesTree("echo \"$(date)\" > f", anchor) {
		t.Errorf("echo \"$(date)\" > f must still target f")
	}
}
