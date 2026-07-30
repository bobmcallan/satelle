package cli

import (
	"fmt"
	"os"
	"strings"
)

// The Claude statusline renderer (sty_4e6f0788): `satelle status --line`.
//
// Claude Code invokes this on session start and on events (new assistant
// message, /compact, permission-mode change, …), debounced at 300ms, and
// CANCELS an in-flight command when a new trigger arrives. Three properties
// follow from that and are the whole design:
//
//   - READ-ONLY. The engaged story comes from currentSeat (cmd_hook.go), which
//     is Leases.List + the pure evaluateSeat — no Reap, no Heartbeat. Never
//     route this through `satelle story seat` / the story-seat-list verb: those
//     reap stale leases, i.e. WRITE, and reaping under a 300ms debounce would
//     thrash lease state against a live session's seat.
//   - NEVER FAILS LOUDLY. Every error becomes one degraded, well-formed line
//     and exit 0. A statusline that printed a stack trace or a partial line
//     would corrupt the operator's status area.
//   - CANCEL-SAFE. No writes, no locks, no temp files; the line is built in
//     memory and handed to a single Write.
//
// Reachability and the port come from probeWebAvailability (web_availability.go,
// sty_fb5e6d96) — the same probed value `satelle status` and the SessionStart
// line use, so no surface can imply a service is up when nothing answers.

// statusLineCommand is the canonical invocation satelle installs into
// .claude/settings.json, and the marker that identifies the entry as
// satelle-owned. Ownership is decided by containment of this string, so an
// operator who wraps it in their own script is still recognised.
const statusLineCommand = "satelle status --line"

// noStorySegment is what the line says when nothing is engaged — explicit
// rather than an empty or dangling `::` segment (AC3).
const noStorySegment = "no story engaged"

// statusLineDegraded is the last-resort line: well-formed, honest, single.
const statusLineDegraded = "satelle: status unavailable"

// hyperlinksEnabled reports whether the terminal renders OSC 8 hyperlinks.
// iTerm2, WezTerm and Kitty do; Terminal.app does not, and emitting the escape
// there would spray raw bytes into the status area — so the default is OFF and
// only a positively identified terminal opts in. A package var so tests pin it.
var hyperlinksEnabled = defaultHyperlinksEnabled

func defaultHyperlinksEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TERM_PROGRAM"))) {
	case "iterm.app", "wezterm":
		return true
	}
	// Kitty advertises itself through TERM rather than TERM_PROGRAM.
	return strings.Contains(strings.ToLower(os.Getenv("TERM")), "kitty")
}

// hyperlink wraps text as an OSC 8 link to url when the terminal supports it,
// and returns text unchanged otherwise — never a raw escape sequence (AC4).
func hyperlink(url, text string) string {
	if !hyperlinksEnabled() {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// serverSegment renders the server half of the line. A live service is a link
// to an address that answers; a dead one is deliberately NOT linked and says so,
// because a clickable link is itself a claim of availability (AC2).
func serverSegment(a webAvailability) string {
	if !a.Resolved {
		return "satelle ?"
	}
	if !a.Live {
		return "satelle " + a.URL() + " down"
	}
	return "satelle " + hyperlink(a.URL(), a.URL())
}

// storySegment renders the engaged half as <story_id>::<stage>, or a plain
// statement that nothing is engaged. stage is the COMMITTED work-item status,
// so the line agrees with `satelle story get` rather than showing a lease
// target mid-transition.
func storySegment(info seatInfo, engaged bool) string {
	if !engaged || strings.TrimSpace(info.ItemID) == "" {
		return noStorySegment
	}
	stage := strings.TrimSpace(info.StoryStatus)
	if stage == "" {
		stage = strings.TrimSpace(info.State)
	}
	if stage == "" {
		stage = "?"
	}
	return info.ItemID + "::" + stage
}

// formatStatusLine joins the two halves into the single line. Pure.
func formatStatusLine(a webAvailability, info seatInfo, engaged bool) string {
	return serverSegment(a) + "  " + storySegment(info, engaged)
}

// renderStatusLine builds the line from live state. It never returns an error:
// a seat lookup failure degrades the story half only, so a reachable server is
// still reported, and a total failure still yields one well-formed line.
func renderStatusLine() string {
	avail := probeWebAvailability()
	info, engaged, err := currentSeat()
	if err != nil {
		// Store unopenable / workflow unresolvable: say nothing about the story
		// rather than guess, but keep the server fact we do have.
		return serverSegment(avail) + "  " + "story ?"
	}
	return formatStatusLine(avail, info, engaged)
}

// runStatusLine writes the statusline and always succeeds. Claude feeds a JSON
// payload on stdin; the renderer does not depend on any of its fields, so stdin
// is simply not read — nothing to block on, and malformed input cannot matter.
func runStatusLine(out *os.File) error {
	line := safeRenderStatusLine()
	if strings.TrimSpace(line) == "" {
		line = statusLineDegraded
	}
	// One Write of one complete line: cancellation mid-run leaves no partial.
	fmt.Fprintln(out, line)
	return nil
}

// safeRenderStatusLine is renderStatusLine with a panic backstop, so no defect
// anywhere beneath it can put a stack trace in the operator's status area.
func safeRenderStatusLine() (line string) {
	defer func() {
		if r := recover(); r != nil {
			line = statusLineDegraded
		}
	}()
	return renderStatusLine()
}
