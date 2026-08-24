package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// callerID is the PreToolUse caller's resolved model and how it was found.
// Key is one of payload_model, tool_use_id, transcript_path, "mtime heuristic",
// or empty when unresolved. A non-heuristic Key must not depend on file mtime.
type callerID struct {
	Transcript string
	Key        string
	Model      string
	Reason     string
}

// callerFS is the file seam resolveCaller uses so tests can swap mtimes
// without touching the clock.
type callerFS interface {
	ReadFile(name string) ([]byte, error)
	Glob(pattern string) ([]string, error)
	ModTime(name string) (int64, error)
}

type osCallerFS struct{}

func (osCallerFS) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }
func (osCallerFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (osCallerFS) ModTime(name string) (int64, error) {
	st, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return st.ModTime().UnixNano(), nil
}

type hookCallerPayload struct {
	Model               string `json:"model"`
	ModelID             string `json:"modelId"`
	ToolUseID           string `json:"tool_use_id"`
	ToolUseIDCamel      string `json:"toolUseId"`
	TranscriptPath      string `json:"transcript_path"`
	TranscriptPathCamel string `json:"transcriptPath"`
	Agent               struct {
		Model string `json:"model"`
	} `json:"agent"`
}

func resolveCaller(raw []byte, fs callerFS) callerID {
	if fs == nil {
		fs = osCallerFS{}
	}
	var p hookCallerPayload
	_ = json.Unmarshal(raw, &p)
	if m := firstNonEmpty(p.Model, p.ModelID, p.Agent.Model); m != "" {
		return callerID{Key: "payload_model", Model: m, Reason: "caller model from payload field"}
	}
	transcript := firstNonEmpty(p.TranscriptPath, p.TranscriptPathCamel)
	toolID := firstNonEmpty(p.ToolUseID, p.ToolUseIDCamel)
	if toolID != "" {
		if id := findToolUseModel(fs, transcript, toolID); id.Model != "" {
			id.Key = "tool_use_id"
			id.Reason = "caller model from the assistant entry carrying tool_use_id " + toolID
			return id
		}
	}
	if transcript != "" && isSubagentTranscript(transcript) {
		if m := lastAssistantModel(fs, transcript); m != "" {
			return callerID{
				Transcript: transcript,
				Key:        "transcript_path",
				Model:      m,
				Reason:     "caller model from payload transcript_path (subagent transcript)",
			}
		}
	}
	if transcript != "" {
		if id, ok := mtimeHeuristicCaller(fs, transcript); ok {
			return id
		}
		if m := lastAssistantModel(fs, transcript); m != "" {
			return callerID{
				Transcript: transcript,
				Key:        "mtime heuristic",
				Model:      m,
				Reason:     "caller model via mtime heuristic (payload transcript)",
			}
		}
	}
	return callerID{Reason: "no caller model resolvable from this payload"}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}

func isSubagentTranscript(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/subagents/")
}

func subagentGlob(transcript string) string {
	base := strings.TrimSuffix(transcript, ".jsonl")
	return filepath.Join(base, "subagents", "*.jsonl")
}

func findToolUseModel(fs callerFS, transcript, toolID string) callerID {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	add(transcript)
	if transcript != "" {
		matches, _ := fs.Glob(subagentGlob(transcript))
		for _, m := range matches {
			add(m)
		}
		if isSubagentTranscript(transcript) {
			matches, _ = fs.Glob(filepath.Join(filepath.Dir(transcript), "*.jsonl"))
			for _, m := range matches {
				add(m)
			}
		}
	}
	for _, p := range paths {
		if m := toolUseModelIn(fs, p, toolID); m != "" {
			return callerID{Transcript: p, Model: m}
		}
	}
	return callerID{}
}

func toolUseModelIn(fs callerFS, path, toolID string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		model, ids := parseAssistantToolUses(line)
		for _, id := range ids {
			if id == toolID {
				return model
			}
		}
	}
	return ""
}

func lastAssistantModel(fs callerFS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		return ""
	}
	last := ""
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if m, _ := parseAssistantToolUses(line); m != "" {
			last = m
		}
	}
	return last
}

func parseAssistantToolUses(line []byte) (model string, ids []string) {
	var rec struct {
		Message struct {
			Model   string          `json:"model"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil {
		return "", nil
	}
	model = strings.TrimSpace(rec.Message.Model)
	if model == "" {
		return "", nil
	}
	var blocks []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return model, nil
	}
	for _, b := range blocks {
		if b.Type == "tool_use" && strings.TrimSpace(b.ID) != "" {
			ids = append(ids, b.ID)
		}
	}
	return model, ids
}

func mtimeHeuristicCaller(fs callerFS, transcript string) (callerID, bool) {
	mainTime, err := fs.ModTime(transcript)
	if err != nil {
		mainTime = 0
	}
	matches, _ := fs.Glob(subagentGlob(transcript))
	newest := ""
	var newestTime int64
	for _, p := range matches {
		t, err := fs.ModTime(p)
		if err != nil {
			continue
		}
		if newest == "" || t >= newestTime {
			newest, newestTime = p, t
		}
	}
	chosen := transcript
	if newest != "" && newestTime > mainTime {
		chosen = newest
	}
	m := lastAssistantModel(fs, chosen)
	if m == "" {
		return callerID{}, false
	}
	return callerID{
		Transcript: chosen,
		Key:        "mtime heuristic",
		Model:      m,
		Reason:     "caller model via mtime heuristic",
	}, true
}
