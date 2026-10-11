package agentcli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Claude Code transcript layout has this one owner: where a session's
// transcript lives, which files are its subagents', and how to tell a subagent's
// transcript path from a parent's. internal/cli's caller resolution and the
// driver-usage reader both call it.
//
//	<config>/projects/<project-slug>/<session-id>.jsonl               the session
//	<config>/projects/<project-slug>/<session-id>/subagents/*.jsonl   its subagents

// ClaudeSubagentGlob is the glob matching the subagent transcripts of the session
// whose transcript is at transcript.
func ClaudeSubagentGlob(transcript string) string {
	base := strings.TrimSuffix(transcript, ".jsonl")
	return filepath.Join(base, "subagents", "*.jsonl")
}

// IsClaudeSubagentTranscript reports whether path is a subagent's transcript.
func IsClaudeSubagentTranscript(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/subagents/")
}

// ClaudeLocateSession resolves a session id to its transcript path: repoRoot's
// project directory, then each hint root's, then every project directory. A
// non-empty reason says the transcript was not found, or was found in more than
// one project, claude-named.
func ClaudeLocateSession(sessionID, repoRoot string, hints []string) (path, reason string) {
	return locateSessionRecord(HarnessClaude, sessionID, filepath.Join(claudeConfigDir(), "projects"),
		claudeProjectSlug, sessionID+".jsonl", append([]string{repoRoot}, hints...))
}

// claudeSubagentTranscripts is the subagent transcripts of the session whose
// transcript is at transcript, oldest first, so the newest is last.
func claudeSubagentTranscripts(transcript string) []string {
	files, _ := filepath.Glob(ClaudeSubagentGlob(transcript))
	modTime := func(p string) time.Time {
		if info, err := os.Stat(p); err == nil {
			return info.ModTime()
		}
		return time.Time{}
	}
	sort.Slice(files, func(i, j int) bool {
		mi, mj := modTime(files[i]), modTime(files[j])
		if mi.Equal(mj) {
			return files[i] < files[j]
		}
		return mi.Before(mj)
	})
	return files
}
