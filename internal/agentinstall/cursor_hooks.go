package agentinstall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The cursor-agent hook scaffold (sty_7d098d50). cursor reads project hooks from
// .cursor/hooks.json, a FLAT file — {"version":1,"hooks":{"<event>":[{"command":…}]}}
// — unlike Claude's matcher groups. The file is the operator's as much as
// satelle's, so install merges into it and remove strips only what satelle owns,
// keeping the user's `version`, unknown keys, key order and their own entries.
//
// This package knows the file's SHAPE and the ownership rule; the commands that
// go in it (the wrapper path, the PATH-prefixed verbs) are the caller's, passed
// as CursorHook values, because they are built from the repo root and the cli's
// own command constants.

// CursorHooksRel is the repo-relative path of cursor's project hooks file.
const CursorHooksRel = ".cursor/hooks.json"

// CursorHook is one satelle-owned hook entry the scaffold wants.
type CursorHook struct {
	Event   string // cursor's own event name, e.g. preToolUse
	Command string // the full command line
	// Role is a substring every command filling this slot carries and no other
	// slot's command does. An owned entry on Event whose command contains Role but
	// differs from Command is a stale form of this slot and is replaced in place.
	Role string
	// TimeoutS, when positive, is the timeout in seconds the entry carries.
	// cursor kills a hook that outlasts its timeout (the default dropped a 75s
	// stop hook, probe 20-slow-default), so a hook that waits sets one. An
	// installed entry with none, or a shorter one, is raised; a longer one an
	// operator set is kept.
	TimeoutS int
}

// IsSatelleOwnedHookCommand reports whether a hook command is satelle-managed.
// It is the one definition install, remove and the cli's own scaffolds share.
func IsSatelleOwnedHookCommand(cmd string) bool {
	return strings.Contains(cmd, "satelle-hook.sh") ||
		strings.Contains(cmd, "satelle hook ") ||
		strings.Contains(cmd, "satelle reindex")
}

// kv is one member of a JSON object, kept in file order.
type kv struct {
	key string
	val json.RawMessage
}

// parseObject reads a JSON object into its members, in order. A duplicate key is
// kept (both members survive a round trip).
func parseObject(raw []byte) ([]kv, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var out []kv
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, kv{key: key, val: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content after the JSON object")
	}
	return out, nil
}

func renderObject(members []kv) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func renderArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

func findMember(members []kv, key string) int {
	for i, m := range members {
		if m.key == key {
			return i
		}
	}
	return -1
}

// entryCommand is the command of one hooks entry, "" when it has none.
func entryCommand(entry json.RawMessage) string {
	var e struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(entry, &e)
	return e.Command
}

// entryTimeout is the timeout, in seconds, of one hooks entry, 0 when it has none.
func entryTimeout(entry json.RawMessage) float64 {
	var e struct {
		Timeout float64 `json:"timeout"`
	}
	_ = json.Unmarshal(entry, &e)
	return e.Timeout
}

// withTimeout returns entry with its timeout raised to want seconds when it has
// none or a shorter one; every other member is kept as it was. want <= 0 leaves
// the entry alone.
func withTimeout(entry json.RawMessage, want int) json.RawMessage {
	if want <= 0 || entryTimeout(entry) >= float64(want) {
		return entry
	}
	members, err := parseObject(entry)
	if err != nil {
		return entry
	}
	val := json.RawMessage(strconv.Itoa(want))
	if i := findMember(members, "timeout"); i >= 0 {
		members[i].val = val
	} else {
		members = append(members, kv{key: "timeout", val: val})
	}
	return renderObject(members)
}

// finishCursorDoc marshals the document indented, with a trailing newline.
func finishCursorDoc(members []kv) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, renderObject(members), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// RenderCursorHooks returns the .cursor/hooks.json bytes after installing want
// into existing (nil or empty for a fresh file). Missing entries are appended; a
// stale form of a wanted slot is replaced in place; everything else — the user's
// version, unknown keys, key order, their own entries — is kept verbatim, so
// rendering an already-installed file returns it unchanged. The error is
// non-nil when existing is not a JSON object whose hooks member is an object of
// arrays; the file is then not safe to rewrite.
func RenderCursorHooks(existing []byte, want []CursorHook) ([]byte, error) {
	var top []kv
	if len(bytes.TrimSpace(existing)) > 0 {
		var err error
		if top, err = parseObject(existing); err != nil {
			return nil, fmt.Errorf("%s: %w", CursorHooksRel, err)
		}
	}
	if findMember(top, "version") < 0 {
		top = append([]kv{{key: "version", val: json.RawMessage("1")}}, top...)
	}
	var hooks []kv
	hi := findMember(top, "hooks")
	if hi >= 0 {
		var err error
		if hooks, err = parseObject(top[hi].val); err != nil {
			return nil, fmt.Errorf("%s: hooks: %w", CursorHooksRel, err)
		}
	}
	for _, w := range want {
		ei := findMember(hooks, w.Event)
		var entries []json.RawMessage
		if ei >= 0 {
			if err := json.Unmarshal(hooks[ei].val, &entries); err != nil {
				return nil, fmt.Errorf("%s: hooks.%s: %w", CursorHooksRel, w.Event, err)
			}
		}
		mine, _ := json.Marshal(map[string]string{"command": w.Command})
		placed := false
		for i, ent := range entries {
			cmd := entryCommand(ent)
			if !IsSatelleOwnedHookCommand(cmd) || !strings.Contains(cmd, w.Role) {
				continue
			}
			if cmd != w.Command {
				// A stale form is replaced whole; an operator's longer timeout survives it.
				entries[i] = mine
				if w.TimeoutS > 0 {
					entries[i] = withTimeout(mine, max(w.TimeoutS, int(entryTimeout(ent))))
				}
			} else {
				entries[i] = withTimeout(ent, w.TimeoutS)
			}
			placed = true
			break
		}
		if !placed {
			entries = append(entries, withTimeout(mine, w.TimeoutS))
		}
		if ei >= 0 {
			hooks[ei].val = renderArray(entries)
		} else {
			hooks = append(hooks, kv{key: w.Event, val: renderArray(entries)})
		}
	}
	hv := renderObject(hooks)
	if hi >= 0 {
		top[hi].val = hv
	} else {
		top = append(top, kv{key: "hooks", val: hv})
	}
	return finishCursorDoc(top)
}

// CursorHooksMissing returns the wanted slots absent from raw, as event names
// (repeated when a slot of that event is missing twice). A file that does not
// parse reports every slot missing.
func CursorHooksMissing(raw []byte, want []CursorHook) []string {
	var missing []string
	top, err := parseObject(raw)
	var hooks []kv
	if err == nil {
		if hi := findMember(top, "hooks"); hi >= 0 {
			hooks, err = parseObject(top[hi].val)
		}
	}
	for _, w := range want {
		found := false
		if err == nil {
			if ei := findMember(hooks, w.Event); ei >= 0 {
				var entries []json.RawMessage
				if json.Unmarshal(hooks[ei].val, &entries) == nil {
					for _, ent := range entries {
						if cmd := entryCommand(ent); IsSatelleOwnedHookCommand(cmd) && strings.Contains(cmd, w.Role) {
							found = true
							break
						}
					}
				}
			}
		}
		if !found {
			missing = append(missing, w.Event)
		}
	}
	return missing
}

// CursorHooksShortTimeout returns the events of wanted slots that carry a
// TimeoutS whose installed entry has none or a shorter one. A slot that is
// absent is CursorHooksMissing's to report, not this one's.
func CursorHooksShortTimeout(raw []byte, want []CursorHook) []string {
	top, err := parseObject(raw)
	if err != nil {
		return nil
	}
	hi := findMember(top, "hooks")
	if hi < 0 {
		return nil
	}
	hooks, err := parseObject(top[hi].val)
	if err != nil {
		return nil
	}
	var short []string
	for _, w := range want {
		ei := findMember(hooks, w.Event)
		if w.TimeoutS <= 0 || ei < 0 {
			continue
		}
		var entries []json.RawMessage
		if json.Unmarshal(hooks[ei].val, &entries) != nil {
			continue
		}
		for _, ent := range entries {
			if cmd := entryCommand(ent); IsSatelleOwnedHookCommand(cmd) && strings.Contains(cmd, w.Role) {
				if entryTimeout(ent) < float64(w.TimeoutS) {
					short = append(short, w.Event)
				}
				break
			}
		}
	}
	return short
}

// CursorHookCommands returns the command of every satelle-owned entry under
// event, in file order. A file that does not parse yields none.
func CursorHookCommands(raw []byte, event string) []string {
	top, err := parseObject(raw)
	if err != nil {
		return nil
	}
	hi := findMember(top, "hooks")
	if hi < 0 {
		return nil
	}
	hooks, err := parseObject(top[hi].val)
	if err != nil {
		return nil
	}
	ei := findMember(hooks, event)
	if ei < 0 {
		return nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(hooks[ei].val, &entries) != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		if cmd := entryCommand(ent); IsSatelleOwnedHookCommand(cmd) {
			out = append(out, cmd)
		}
	}
	return out
}

// RemoveCursorHooks strips every satelle-owned entry from raw, keeping the rest
// byte-for-byte in value and in order. empty is true when nothing but the
// `version` member would remain — the file then held nothing of the user's and
// the caller deletes it. changed is false when raw carried no satelle entry.
func RemoveCursorHooks(raw []byte) (out []byte, empty, changed bool, err error) {
	top, err := parseObject(raw)
	if err != nil {
		return nil, false, false, fmt.Errorf("%s: %w", CursorHooksRel, err)
	}
	hi := findMember(top, "hooks")
	if hi >= 0 {
		hooks, err := parseObject(top[hi].val)
		if err != nil {
			return nil, false, false, fmt.Errorf("%s: hooks: %w", CursorHooksRel, err)
		}
		var keptEvents []kv
		for _, ev := range hooks {
			var entries []json.RawMessage
			if json.Unmarshal(ev.val, &entries) != nil {
				keptEvents = append(keptEvents, ev) // not a list we understand: the user's
				continue
			}
			var kept []json.RawMessage
			for _, ent := range entries {
				if IsSatelleOwnedHookCommand(entryCommand(ent)) {
					changed = true
					continue
				}
				kept = append(kept, ent)
			}
			if len(kept) == len(entries) {
				keptEvents = append(keptEvents, ev)
			} else if len(kept) > 0 {
				keptEvents = append(keptEvents, kv{key: ev.key, val: renderArray(kept)})
			}
		}
		if len(keptEvents) == 0 {
			top = append(top[:hi:hi], top[hi+1:]...)
		} else {
			top[hi].val = renderObject(keptEvents)
		}
	}
	if !changed {
		return raw, false, false, nil
	}
	empty = true
	for _, m := range top {
		if m.key != "version" {
			empty = false
		}
	}
	if empty {
		return nil, true, true, nil
	}
	out, err = finishCursorDoc(top)
	return out, false, true, err
}
