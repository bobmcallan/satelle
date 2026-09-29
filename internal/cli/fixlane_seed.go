package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// fixLaneScaffoldBlock is the [fix_lane] section a fresh init seeds and a heal
// appends when the section is absent (sty_4b694872). Product surface is the
// repo's own opinion, so the binary can only seed the DECLARATION, not its
// answer: product_surface starts empty, which leaves the lane CLOSED (every
// claim refused, class undeclared-bound) until the operator names what is
// product. Fail-closed is only real if the declaration is easy to make and never
// silently overwritten, so this block is written once, and never rewritten.
//
// The ceiling shown in the comment is RENDERED from the embedded default
// (config.EmbeddedFixLane), never spelled here: the bound has one source.
func fixLaneScaffoldBlock() string {
	return fmt.Sprintf(fixLaneScaffoldTemplate, config.EmbeddedFixLane().MaxLines)
}

const fixLaneScaffoldTemplate = `
# [fix_lane] — the scoped in-loop fix lane (sty_4b694872): a driver may record a
# typed claim ('satelle fix claim') and make ONE small, self-evident edit without
# a full engage. The bound is repo configuration, never a number in the binary.
# product_surface lists the path globs the lane may NEVER touch because they are
# the shipped product (** = any depth; a glob with no / matches a basename).
# Left empty the lane is CLOSED: every claim is refused (undeclared-bound) —
# declare your product paths to open it. Gate skills, reviewer rubrics,
# workflows, principles, the constitution and this config are always refused:
# satelle derives them from where YOUR repo keeps its substrate (data_dir,
# substrate_roots); the lists below ADD to them, never subtract. max_lines is the
# largest size bound, in changed lines, a claim may declare (default from the
# embedded fix_lane.toml). Existing values are never overwritten by init.
# Analyse the lane with 'satelle fix report'. See 'satelle help fix-lane'.
[fix_lane]
product_surface = []
# max_lines = %d
# gate_skills = []
# reviewer_rubrics = []
# workflows = []
# principles = []
# repo_config = []
`

// healFixLane appends the [fix_lane] block to an existing satelle.toml ONLY when
// the section is absent. A present section — however authored, empty or not — is
// left byte-untouched. Reports whether it wrote.
func healFixLane(dataDir string) (bool, error) {
	path := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	content := string(raw)
	if config.HasSection(content, "fix_lane") {
		return false, nil
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content+fixLaneScaffoldBlock()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
