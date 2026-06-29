package verb

// storyDir is the per-repo directory that holds story ATTACHMENTS
// (<storyDir>/<id>/…); see storydocs.go. The per-story markdown MIRROR that once
// lived here (<storyDir>/<id>.md) was removed — the SQLite store is the sole
// story store (sty_fa1e02e1). Empty disables attachments (e.g. tests that don't
// opt in).
var storyDir string

// SetStoryDir wires the directory that holds per-story attachments.
func SetStoryDir(dir string) { storyDir = dir }
