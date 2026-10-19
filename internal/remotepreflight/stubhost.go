package remotepreflight

import "context"

// StubNote is the detail a check carries when the host did not run its command.
const StubNote = "not run: stub host"

// StubHost is the host a local test run uses: it runs nothing. A probe's argv may
// act on a real estate under the developer's identity, so a test run reports each
// probe as not run instead. The package imports no process-running code, so this
// cannot be made to run one.
type StubHost struct{}

// Exec reports the command as not run.
func (StubHost) Exec(context.Context, []string) (Outcome, error) {
	return Outcome{Ran: false, Note: StubNote}, nil
}
