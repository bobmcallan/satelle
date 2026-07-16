package verb

import "path/filepath"

// storyDir is the per-repo directory that holds story ATTACHMENTS
// (<storyDir>/<id>/…); see storydocs.go. The per-story markdown MIRROR that once
// lived here (<storyDir>/<id>.md) was removed — the SQLite store is the sole
// story store (sty_fa1e02e1). Empty disables attachments (e.g. tests that don't
// opt in). Lives on the runtime plane (sty_4660bbe1).
var storyDir string

// dataDir is the authored substrate root (<repo>/.satelle). Used when a verb
// needs documents/ or other authored paths that no longer share a parent with
// storyDir after the runtime plane split (sty_4660bbe1).
var dataDir string

// backupsDir is the root for mandatory backups (stories + tasks archives).
// Explicit rather than derived from storyDir/taskDir parents — those dirs now
// live on different planes (runtime vs authored) so sibling derivation would
// split them (sty_4660bbe1). Empty falls back to filepath.Dir(storyDir)/backups
// or filepath.Dir(taskDir)/backups for tests that only set one of the dirs.
var backupsDir string

// SetStoryDir wires the directory that holds per-story attachments.
func SetStoryDir(dir string) { storyDir = dir }

// SetDataDir wires the authored substrate root (toml, documents, workflows, …).
func SetDataDir(dir string) { dataDir = dir }

// SetBackupsDir wires the runtime backups root (<runtime>/backups).
func SetBackupsDir(dir string) { backupsDir = dir }

// resolveBackupsDir returns the configured backups root, or a sibling of the
// preferred anchor when unset (keeps unit tests that only SetStoryDir/SetTaskDir working).
func resolveBackupsDir(anchor string) string {
	if backupsDir != "" {
		return backupsDir
	}
	if anchor != "" {
		return filepath.Join(filepath.Dir(anchor), "backups")
	}
	return ""
}
