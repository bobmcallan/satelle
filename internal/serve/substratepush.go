// Substrate push leg (sty_a66e1846).
//
// Work state reaches the developer's personal project on its own: the ingest
// Pusher and the reconcile pass exec `satelle sync workstate push`. Authored
// substrate (config areas and documents) used to move only on an explicit
// `satelle sync`, so an edit under .satelle, a reindex or a skill change reached
// nothing until the developer remembered. This leg closes that on the same
// seam: each reconcile pass execs the two push verbs, after the work-state push.
//
// serve decides nothing here. Whether an area is opted in, whether the repo is
// bound, and whether the machine is behind, diverged or unmerged are the verbs'
// calls — an all-local repo gets a no-op exit zero, a refused area records its
// state in syncstate and exits non-zero. serve only schedules, runs, and logs a
// failure once per streak (Reconciler.reportSubstrate).
package serve

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// substratePushArgs is the exact argv of each substrate push the loop runs.
// --snapshot-only is what makes an unattended push safe: it never falls back to
// the plain upload that cannot tell a diverged machine from an in-step one. The
// loop never pulls or deploys, and never passes --force (which would override a
// newer hosted snapshot) or --prune (which would drop hosted files).
var substratePushArgs = [][]string{
	{"sync", "config", "push", "--snapshot-only"},
	{"sync", "documents", "push", "--snapshot-only"},
}

// substratePushViaCLI runs every substrate push inside repoPath. A refusal in one
// does not stop the other; the errors are joined.
func substratePushViaCLI(ctx context.Context, repoPath string) error {
	bin, err := satelleBinary()
	if err != nil {
		return err
	}
	var errs []error
	for _, args := range substratePushArgs {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out))))
		}
	}
	return errors.Join(errs...)
}
