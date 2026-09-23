// Scratch gives every dispatched or live agent session its own throwaway
// directory outside the repo tree (sty_e7aaf8b1). No skill/principle needs to
// tell an agent where to write evidence or debris — TMPDIR/SATELLE_SCRATCH
// name it by mechanism, and the charter states it in every isolated briefing.
package agentstep

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
)

// dispatchID is a timestamp plus a random suffix — unique per call, and sortable.
func dispatchID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf))
}

// storyComponent is the path segment for a scratch dir's story leg. An empty
// storyID (a dispatch with no item, e.g. a bootstrap check) still gets an
// isolated directory rather than colliding with every other adhoc caller.
func storyComponent(storyID string) string {
	if storyID == "" {
		return "adhoc-" + dispatchID()
	}
	return storyID
}

// newScratch creates <tmp>/satelle/<repo-key>/<story>/<dispatch-id>/, mode
// 0700, and returns its path. repoRoot may be empty in tests; os.TempDir()
// still anchors the tree.
func newScratch(repoRoot, storyID string) (string, error) {
	dir := filepath.Join(config.StoryScratchDirIn(os.TempDir(), repoRoot, storyComponent(storyID)), dispatchID())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("scratch: create %s: %w", dir, err)
	}
	return dir, nil
}

// scratchEnv returns the TMPDIR / SATELLE_SCRATCH pair for dir. Both keys are
// RESERVED — they win over any binding-authored env of the same name, since a
// dispatch's scratch location is mechanism, not something a binding's [env]
// table should be able to redirect.
func scratchEnv(dir string) map[string]string {
	if dir == "" {
		return nil
	}
	return map[string]string{
		"TMPDIR":          dir,
		config.ScratchEnv: dir,
	}
}

// scratchBriefing is the charter sentence every isolated/live agent receives
// naming its scratch directory. Generated text, appended in buildRequest — no
// skill or principle file carries this instruction (sty_e7aaf8b1 AC2).
func scratchBriefing(dir string) string {
	if dir == "" {
		return ""
	}
	return fmt.Sprintf("Your scratch directory is `%s` ($%s, also $TMPDIR). "+
		"Write every temporary or evidence file there, never in the repository tree. "+
		"To attach text to the story, use `satelle story attach <id> --name ... --body \"...\"`, "+
		"or `--file $%s/<file>` for a large document.",
		dir, config.ScratchEnv, config.ScratchEnv)
}

// finishScratch disposes of a dispatch's scratch directory: removed when the
// dispatch succeeded and nothing was swept into it, kept (for diagnosis, or
// because leftovers now live under it) otherwise.
func finishScratch(dir string, keep bool) {
	if dir == "" || keep {
		return
	}
	_ = os.RemoveAll(dir)
}
