package cli

import (
	"path/filepath"
	"strings"
	"unicode"
)

// bashscan is a pure, quote-aware best-effort Bash command classifier used by
// the PreToolUse commitgate for cross-repo containment and tokenized commit/push
// detection (sty_aadd4d6c).
//
// Honest scope: reminder and boundary, not a sandbox. Variable expansion, eval,
// xargs, command substitution, symlink escapes, and a bypassed hook are out of
// scope. When a form is ambiguous, classifiers return no target / false rather
// than guess (false positives are worse than missed exotic forms).

// bashTok is one lexical token from a best-effort scan.
type bashTok struct {
	// Kind is "word" for an unquoted shell word, "string" for a quoted span
	// (single/double/dollar-quote/heredoc body collapsed to one opaque token),
	// or "op" for a segment operator (;, &&, ||, |, newline).
	Kind  string
	Value string // raw value for word; empty or placeholder for string/op
	Op    string // for Kind=="op": ";", "&&", "||", "|", "\n"
}

// tokenizeBash splits a Bash payload into tokens. Quoted spans and heredoc
// bodies become a single opaque "string" token so prose (e.g. a story --body)
// cannot contribute matching word tokens to commit/push detection.
func tokenizeBash(command string) []bashTok {
	var out []bashTok
	i := 0
	n := len(command)
	for i < n {
		// Whitespace (except newline, which is a segment op).
		if command[i] == ' ' || command[i] == '\t' || command[i] == '\r' {
			i++
			continue
		}
		// Newline segment boundary.
		if command[i] == '\n' {
			out = append(out, bashTok{Kind: "op", Op: "\n"})
			i++
			continue
		}
		// Operators: && || ; |
		if command[i] == '&' && i+1 < n && command[i+1] == '&' {
			out = append(out, bashTok{Kind: "op", Op: "&&"})
			i += 2
			continue
		}
		if command[i] == '|' && i+1 < n && command[i+1] == '|' {
			out = append(out, bashTok{Kind: "op", Op: "||"})
			i += 2
			continue
		}
		if command[i] == ';' {
			out = append(out, bashTok{Kind: "op", Op: ";"})
			i++
			continue
		}
		if command[i] == '|' {
			out = append(out, bashTok{Kind: "op", Op: "|"})
			i++
			continue
		}
		// Heredoc: <<[-]['"]WORD['"] … WORD
		if command[i] == '<' && i+1 < n && command[i+1] == '<' {
			end, ok := consumeHeredoc(command, i)
			if ok {
				out = append(out, bashTok{Kind: "string", Value: "HEREDOC"})
				i = end
				continue
			}
		}
		// Single-quoted string.
		if command[i] == '\'' {
			j := i + 1
			for j < n && command[j] != '\'' {
				j++
			}
			if j < n {
				j++ // closing quote
			}
			out = append(out, bashTok{Kind: "string", Value: command[i:j]})
			i = j
			continue
		}
		// Double-quoted string (best-effort: \" and \\ escapes).
		if command[i] == '"' {
			j := i + 1
			for j < n {
				if command[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if command[j] == '"' {
					j++
					break
				}
				j++
			}
			out = append(out, bashTok{Kind: "string", Value: command[i:j]})
			i = j
			continue
		}
		// Dollar-single-quote $'...'
		if command[i] == '$' && i+1 < n && command[i+1] == '\'' {
			j := i + 2
			for j < n {
				if command[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if command[j] == '\'' {
					j++
					break
				}
				j++
			}
			out = append(out, bashTok{Kind: "string", Value: command[i:j]})
			i = j
			continue
		}
		// Unquoted word: runs until whitespace, op, quote, or redirect marker start
		// that begins a separate token (we keep > / >> / < glued to the word when
		// they are part of a redirect like >file without space; spaced redirects
		// are handled as separate words).
		j := i
		for j < n {
			c := command[j]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' ||
				c == ';' || c == '|' || c == '&' ||
				c == '\'' || c == '"' {
				break
			}
			// Stop before && / || (already handled if at start).
			if c == '&' || (c == '|' && j+1 < n && command[j+1] == '|') {
				break
			}
			// Heredoc start inside a word is rare; treat < as word content unless <<.
			if c == '<' && j+1 < n && command[j+1] == '<' {
				break
			}
			j++
		}
		if j > i {
			out = append(out, bashTok{Kind: "word", Value: command[i:j]})
			i = j
			continue
		}
		// Fallback: single unknown char as word.
		out = append(out, bashTok{Kind: "word", Value: string(command[i])})
		i++
	}
	return out
}

// consumeHeredoc advances past a heredoc opener and its body. Returns the index
// after the terminating delimiter line, or (start, false) if it cannot parse.
func consumeHeredoc(s string, start int) (int, bool) {
	// start points at first '<' of <<
	i := start + 2
	n := len(s)
	if i < n && s[i] == '-' {
		i++ // <<-
	}
	// Optional quotes around delimiter.
	for i < n && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= n {
		return start, false
	}
	delim := ""
	if s[i] == '\'' || s[i] == '"' {
		q := s[i]
		i++
		j := i
		for j < n && s[j] != q {
			j++
		}
		delim = s[i:j]
		if j < n {
			j++
		}
		i = j
	} else {
		j := i
		for j < n && !unicode.IsSpace(rune(s[j])) && s[j] != ';' && s[j] != '|' && s[j] != '&' {
			j++
		}
		delim = s[i:j]
		i = j
	}
	if delim == "" {
		return start, false
	}
	// Skip rest of the opener line to newline.
	for i < n && s[i] != '\n' {
		i++
	}
	if i < n {
		i++ // past newline
	}
	// Body until a line that is exactly delim.
	for i < n {
		lineStart := i
		for i < n && s[i] != '\n' {
			i++
		}
		line := s[lineStart:i]
		if line == delim {
			if i < n {
				i++ // consume newline after delimiter
			}
			return i, true
		}
		if i < n {
			i++
		}
	}
	return i, true // unterminated: consume rest
}

// segmentWords splits tokens into command segments at ; && || | newline.
// Each segment is the list of word tokens only (strings are kept as opaque
// placeholders so position is preserved for flag parsing, but prose does not
// match as verb names).
func segmentWords(toks []bashTok) [][]bashTok {
	var segs [][]bashTok
	var cur []bashTok
	flush := func() {
		if len(cur) > 0 {
			segs = append(segs, cur)
			cur = nil
		}
	}
	for _, t := range toks {
		if t.Kind == "op" {
			flush()
			continue
		}
		cur = append(cur, t)
	}
	flush()
	return segs
}

// wordsOnly returns the Value of word-kind tokens in a segment (for classifiers
// that need bare shell words). String tokens are omitted so quoted prose never
// matches as a command word.
func wordsOnly(seg []bashTok) []string {
	var w []string
	for _, t := range seg {
		if t.Kind == "word" && t.Value != "" {
			w = append(w, t.Value)
		}
	}
	return w
}

// isGitCommitOrPush reports whether any segment is a git commit or push after
// skipping known global options (-C DIR, -c K=V, --git-dir=, …). Quote-aware:
// prose inside quotes does not match.
func isGitCommitOrPush(command string) bool {
	for _, sub := range gitSubcommands(command) {
		if sub == "commit" || sub == "push" {
			return true
		}
	}
	return false
}

// gitSubcommands returns each git subcommand token found in the bash payload
// (e.g. "push", "commit", "status"), quote-aware and skipping global options.
// Used by the engage commitgate and the opt-in [gate.command_allow] step policy.
func gitSubcommands(command string) []string {
	var out []string
	for _, seg := range segmentWords(tokenizeBash(command)) {
		if sub, ok := segmentGitSubcommand(wordsOnly(seg)); ok {
			out = append(out, sub)
		}
	}
	return out
}

// segmentGitSubcommand returns the first non-option word after git as the
// subcommand (lowercased).
func segmentGitSubcommand(words []string) (string, bool) {
	if len(words) == 0 {
		return "", false
	}
	cmd := words[0]
	base := filepath.Base(cmd)
	// Strip a trailing path form: /usr/bin/git or ./git
	if base != "git" && cmd != "git" {
		return "", false
	}
	i := 1
	for i < len(words) {
		w := words[i]
		switch {
		case w == "-C" || w == "-c":
			// Takes a following argument.
			i += 2
			continue
		case strings.HasPrefix(w, "--git-dir=") ||
			strings.HasPrefix(w, "--work-tree=") ||
			strings.HasPrefix(w, "--exec-path=") ||
			strings.HasPrefix(w, "--namespace="):
			i++
			continue
		case w == "--git-dir" || w == "--work-tree" || w == "--exec-path" || w == "--namespace":
			i += 2
			continue
		case w == "-p" || w == "--paginate" || w == "--no-pager" ||
			w == "--bare" || w == "--literal-pathspecs" ||
			w == "--glob-pathspecs" || w == "--noglob-pathspecs" ||
			w == "--icase-pathspecs" || w == "--no-optional-locks" ||
			w == "--no-replace-objects":
			i++
			continue
		case strings.HasPrefix(w, "-") && w != "--":
			// Unknown global option: skip conservatively without consuming an arg
			// (avoids treating a verb as an option value).
			i++
			continue
		default:
			// First non-option word is the subcommand.
			return strings.ToLower(w), true
		}
	}
	return "", false
}

// segmentIsGitCommitOrPush classifies one segment's word list.
func segmentIsGitCommitOrPush(words []string) bool {
	sub, ok := segmentGitSubcommand(words)
	return ok && (sub == "commit" || sub == "push")
}

// isFusedEngageAndCommit reports whether a Bash payload both engages a story
// (story set … --status) and runs git commit/push — using tokenized matching so
// prose does not false-trigger either side.
func isFusedEngageAndCommit(command string) bool {
	if !isGitCommitOrPush(command) {
		return false
	}
	for _, seg := range segmentWords(tokenizeBash(command)) {
		if segmentIsStoryEngage(wordsOnly(seg)) {
			return true
		}
	}
	return false
}

// segmentIsStoryEngage is true when words look like satelle story set … --status.
func segmentIsStoryEngage(words []string) bool {
	if len(words) < 4 {
		return false
	}
	// Allow path forms ending in satelle.
	if filepath.Base(words[0]) != "satelle" && words[0] != "satelle" {
		return false
	}
	if words[1] != "story" || words[2] != "set" {
		return false
	}
	for _, w := range words[3:] {
		if w == "--status" || strings.HasPrefix(w, "--status=") {
			return true
		}
	}
	return false
}

// dirFlagTable maps command basename → option letters that take a directory
// argument (e.g. git -C DIR). Only named tools; never guessed per tool.
var dirFlagTable = map[string][]string{
	"git":  {"-C"},
	"make": {"-C"},
	"tar":  {"-C"},
}

// mutationVerbs are simple path-mutating commands whose path arguments (when
// they resolve outside the anchor) are containment targets.
var mutationVerbs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "touch": true, "mkdir": true,
	"install": true, "truncate": true, "dd": true, "sed": true, "tee": true,
	"chmod": true, "chown": true, "ln": true, "rsync": true,
}

// mutationTargets returns absolute paths a Bash command would mutate that fall
// outside anchor (pure candidate extraction — no FS stats). Empty when nothing
// escapes or classification is ambiguous. The foreign-tree decision lives in
// containment.go (foreignTreeTarget); this function only extracts candidates.
//
// Each segment carries a working dir seeded from anchor and updated by cd.
// Targets come from: named -C dir flags; redirect/tee destinations; DEST path
// args of mutation verbs; and foreign satelle progress verbs (story/task set
// --status). Read verbs and story/task create contribute no target. In-home
// paths are filtered here so the FS layer never stats them.
func mutationTargets(command, anchor string) []string {
	anchor = filepath.Clean(anchor)
	if anchor == "" || anchor == "." {
		return nil
	}
	var escaped []string
	seen := map[string]bool{}
	add := func(abs string) {
		abs = filepath.Clean(abs)
		if abs == "" || seen[abs] {
			return
		}
		if withinRoot(anchor, abs) {
			return
		}
		seen[abs] = true
		escaped = append(escaped, abs)
	}

	cwd := anchor
	for _, seg := range segmentWords(tokenizeBash(command)) {
		words := wordsOnly(seg)
		if len(words) == 0 {
			continue
		}
		// Redirect targets on the full segment text (spaced and glued forms).
		for _, r := range redirectTargets(seg, cwd) {
			add(r)
		}

		cmd := filepath.Base(words[0])
		// Strip env assignments: FOO=bar cmd …
		idx := 0
		for idx < len(words) && strings.Contains(words[idx], "=") && !strings.HasPrefix(words[idx], "-") {
			// Only treat as assignment if it looks like NAME=value before command.
			eq := strings.IndexByte(words[idx], '=')
			if eq <= 0 {
				break
			}
			name := words[idx][:eq]
			if !isShellIdent(name) {
				break
			}
			idx++
		}
		if idx >= len(words) {
			continue
		}
		words = words[idx:]
		cmd = filepath.Base(words[0])

		// cd DIR — update working dir for later relative paths. cd alone is not
		// a mutation target: `cd /other && satelle story create` must stay
		// allowed (AC2); `cd /other && rm f` is denied via the rm path under
		// the updated cwd (AC1).
		if cmd == "cd" {
			if len(words) >= 2 {
				cwd = resolvePath(cwd, words[1])
			}
			continue
		}

		// satelle foreign verb classification.
		if cmd == "satelle" || filepath.Base(words[0]) == "satelle" {
			if t, ok := foreignSatelleVerb(words, cwd); ok {
				add(t)
			}
			// Still check -C style if any (satelle has no -C today).
			continue
		}

		// Named dir flags (git -C, make -C, tar -C).
		if flags, ok := dirFlagTable[cmd]; ok {
			for i := 1; i < len(words); i++ {
				w := words[i]
				for _, f := range flags {
					if w == f && i+1 < len(words) {
						dir := resolvePath(cwd, words[i+1])
						if !withinRoot(anchor, dir) {
							add(dir)
						}
						// For git -C outside, the dir itself is the mutation home.
						i++
						break
					}
				}
			}
		}

		// Mutation verbs: path-like args after options.
		if mutationVerbs[cmd] {
			for _, p := range mutationPathArgs(words, cwd) {
				add(p)
			}
		}
	}
	return escaped
}

// foreignSatelleVerb returns a foreign target dir when the satelle segment is a
// progress/engage verb that should not run from a foreign session. Create and
// read verbs return ok=false (allowed cross-repo). When the command does not
// target a foreign path explicitly, ok is false — the session anchor check is
// the caller's responsibility for "progress from foreign session".
//
// Classification only: story/task set with --status against an explicit -C /
// chdir is rare; for pure "satelle story set … --status" with no path, we do not
// invent a foreign dir (AC2: engage from foreign session is principle-flagged /
// denied by separate session rules). Explicit paths under another tree still
// surface via redirect/mutation paths elsewhere.
func foreignSatelleVerb(words []string, cwd string) (string, bool) {
	if len(words) < 3 {
		return "", false
	}
	if filepath.Base(words[0]) != "satelle" {
		return "", false
	}
	// satelle [global flags] <noun> <verb> …
	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") {
		// --flag=value or -f value (best-effort skip one arg for known takers).
		if words[i] == "--config" || words[i] == "-c" || words[i] == "--data-dir" {
			i += 2
			continue
		}
		i++
	}
	if i+1 >= len(words) {
		return "", false
	}
	noun, verb := words[i], words[i+1]
	rest := words[i+2:]

	// Create is always allowed (no target).
	if verb == "create" && (noun == "story" || noun == "task") {
		return "", false
	}
	// Read-ish verbs: no target.
	switch verb {
	case "get", "list", "show", "docs", "doc", "cost", "lessons", "seat", "log":
		return "", false
	}
	// Progress: story/task set with --status — if an absolute/relative path
	// appears that escapes, report it; otherwise no path target (principle).
	if verb == "set" && (noun == "story" || noun == "task") {
		hasStatus := false
		for _, w := range rest {
			if w == "--status" || strings.HasPrefix(w, "--status=") {
				hasStatus = true
			}
		}
		if !hasStatus {
			return "", false
		}
		// Look for path-like args that escape (rare CLI forms).
		for _, w := range rest {
			if strings.HasPrefix(w, "-") {
				continue
			}
			// Skip IDs (sty_… / tsk_…).
			if strings.HasPrefix(w, "sty_") || strings.HasPrefix(w, "tsk_") {
				continue
			}
			if looksLikePath(w) {
				abs := resolvePath(cwd, w)
				if !withinRoot(cwd, abs) || filepath.IsAbs(w) {
					// Only report when clearly outside caller's cwd/anchor context —
					// caller filters with withinRoot(anchor, …).
					return abs, true
				}
			}
		}
		return "", false
	}
	return "", false
}

// looksLikePath is a conservative heuristic: absolute, ./, ../, or contains /.
func looksLikePath(s string) bool {
	if s == "" {
		return false
	}
	if filepath.IsAbs(s) || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	return strings.Contains(s, "/")
}

// destLastVerbs treat only the last non-option positional as a mutation target;
// earlier positionals are SOURCES (reads) and must not trip containment
// (sty_a8454d10 AC3). With -t/--target-directory DIR, DIR is the sole target.
var destLastVerbs = map[string]bool{
	"cp": true, "mv": true, "rsync": true, "ln": true, "install": true,
}

// mutationPathArgs extracts DEST path arguments from a mutation verb segment.
// For dest-last verbs (cp/mv/rsync/ln/install), only the destination contributes;
// sources are legitimate cross-repo reads the fence must not deny.
func mutationPathArgs(words []string, cwd string) []string {
	if len(words) < 2 {
		return nil
	}
	cmd := filepath.Base(words[0])
	var out []string
	// dd of=PATH
	if cmd == "dd" {
		for _, w := range words[1:] {
			if strings.HasPrefix(w, "of=") {
				out = append(out, resolvePath(cwd, strings.TrimPrefix(w, "of=")))
			}
		}
		return out
	}

	// dest-last family: collect -t DIR first; else only the last positional.
	if destLastVerbs[cmd] {
		var positionals []string
		skipNext := false
		for i := 1; i < len(words); i++ {
			w := words[i]
			if skipNext {
				skipNext = false
				continue
			}
			if strings.HasPrefix(w, "-") {
				if w == "-t" || w == "--target-directory" {
					if i+1 < len(words) {
						out = append(out, resolvePath(cwd, words[i+1]))
						skipNext = true
					}
					continue
				}
				// Options that take a value (best-effort skip).
				if w == "--backup" || w == "-S" || w == "--suffix" ||
					w == "-t" || strings.HasPrefix(w, "--target-directory=") {
					if !strings.Contains(w, "=") {
						skipNext = true
					}
				}
				continue
			}
			positionals = append(positionals, w)
		}
		// -t already populated out; do not also emit positionals as targets.
		if len(out) > 0 {
			return out
		}
		// ln with a single positional links into cwd — no outside target.
		if len(positionals) == 0 {
			return nil
		}
		if len(positionals) == 1 {
			// Destination is cwd-relative implied link name, or a single dest —
			// treat as dest under cwd (in-home filter drops it if so).
			return []string{resolvePath(cwd, positionals[0])}
		}
		// Last positional is the destination; sources are ignored.
		return []string{resolvePath(cwd, positionals[len(positionals)-1])}
	}

	// All-args family: rm/touch/mkdir/chmod/chown/truncate/sed/tee — every
	// non-option arg is a mutation target.
	skipNext := false
	for i := 1; i < len(words); i++ {
		w := words[i]
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(w, "-") {
			if cmd == "sed" && (w == "-e" || w == "-f") {
				skipNext = true
			}
			if (cmd == "rm" || cmd == "mkdir" || cmd == "chmod" || cmd == "chown") &&
				(w == "-t" || w == "--target-directory") {
				skipNext = true
			}
			continue
		}
		out = append(out, resolvePath(cwd, w))
	}
	return out
}

// redirectTargets finds >file, >>file, > file, and tee destinations.
// Fd-duplication forms (n>&m, n>&-, >&m) contribute no file target — tokenizeBash
// splits on '&', so "2>&1" arrives as words "2>", "&", "1" (sty_74c0556f).
func redirectTargets(seg []bashTok, cwd string) []string {
	var out []string
	// Reconstruct a minimal stream of word values including glued redirects.
	words := wordsOnly(seg)
	for i := 0; i < len(words); i++ {
		w := words[i]
		// tee [opts] FILE…
		if filepath.Base(w) == "tee" {
			for j := i + 1; j < len(words); j++ {
				if strings.HasPrefix(words[j], "-") {
					continue
				}
				out = append(out, resolvePath(cwd, words[j]))
			}
		}
		// Glued: >file or >>file or 2>file
		if strings.Contains(w, ">") {
			if p := gluedRedirectPath(w); p != "" {
				out = append(out, resolvePath(cwd, p))
			}
		}
		// Spaced: > file / >> file / 1> file / 2> & 1 (fd dup) / > & file (csh)
		if w == ">" || w == ">>" || w == "1>" || w == "2>" || w == "&>" || w == ">&" {
			if i+1 >= len(words) || strings.HasPrefix(words[i+1], ">") {
				continue
			}
			next := words[i+1]
			// After a redirect op, "&" is either n>&m (fd duplication) or csh
			// ">& file" / "> & file" (file target is the word after "&").
			if next == "&" {
				if i+2 < len(words) && isFdDupTarget(words[i+2]) {
					i += 2 // skip "&" and the fd / "-"
					continue
				}
				if i+2 < len(words) {
					out = append(out, resolvePath(cwd, words[i+2]))
					i += 2
					continue
				}
				i++ // trailing bare "&" after redirect — no file
				continue
			}
			// One-token ">&" / "&>" followed by fd or path (defensive; tokenizer
			// usually splits ">&m" at '&').
			if w == ">&" && isFdDupTarget(next) {
				i++
				continue
			}
			out = append(out, resolvePath(cwd, next))
			i++
		}
	}
	return out
}

// isFdDupTarget reports whether s is a bash fd-duplication target: all digits
// (e.g. "1", "2"), "-" (close the fd: n>&-), or digits with a single trailing
// "-" (fd-move: n>&m-).
func isFdDupTarget(s string) bool {
	if s == "-" {
		return true
	}
	if s == "" {
		return false
	}
	// Fd-move form: "1-", "2-"
	if strings.HasSuffix(s, "-") && len(s) > 1 {
		s = s[:len(s)-1]
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// gluedRedirectPath extracts the path from tokens like ">file", "2>>file", ">>/abs".
// Bare "&" and "&"-led fd remainders (">&1") are not file paths.
func gluedRedirectPath(w string) string {
	// Find last redirect operator sequence and take the remainder.
	idx := strings.LastIndex(w, ">>")
	if idx >= 0 {
		return gluedRedirectRemainder(w[idx+2:])
	}
	idx = strings.LastIndex(w, ">")
	if idx >= 0 {
		return gluedRedirectRemainder(w[idx+1:])
	}
	return ""
}

func gluedRedirectRemainder(rest string) string {
	if rest == "" || rest == "&" {
		return ""
	}
	// n>&m glued remainder after '>': "&1", "&-" — not a file.
	if strings.HasPrefix(rest, "&") && isFdDupTarget(rest[1:]) {
		return ""
	}
	return rest
}

// resolvePath makes a path absolute against cwd (no symlink resolution).
func resolvePath(cwd, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Strip simple surrounding quotes if tokenizer left them (shouldn't for words).
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func isShellIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}
