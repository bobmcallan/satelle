package cli

import (
	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

func init() {
	register(newBoardCmd())
}

// newBoardCmd builds `satelle board`: every message in the repo with its state.
// The help is the documentation of the four states.
func newBoardCmd() *cobra.Command {
	var state, session, story string
	var overdue bool
	cmd := &cobra.Command{
		Use:   "board",
		Short: "List the messages on every story with their state and overdue flag (read-only)",
		Long: `Every message left with satelle story message, across every story, oldest first:
sender, addressee, state, deadline and an overdue flag. Local — it reads this
machine's ledger and never contacts satelle.dev.

A message is in exactly one state:
  waiting        nothing has marked it yet
  delivered      it reached its addressee (not proof it was read)
  answered       the addressee replied (--reply-to) or it was marked so
  undeliverable  it cannot reach its addressee

Overdue is no state: it is derived at read time — the deadline has passed and
the message is not answered. Change a state with satelle story message-mark.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := map[string]any{}
			if state != "" {
				req["state"] = state
			}
			if overdue {
				req["overdue"] = true
			}
			if session != "" {
				req["session"] = session
			}
			if story != "" {
				req["story"] = story
			}
			return dispatch(cmd, "board-list", req)
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "only this state: waiting | delivered | answered | undeliverable")
	cmd.Flags().BoolVar(&overdue, "overdue", false, "only messages past their deadline and not answered")
	cmd.Flags().StringVar(&session, "session", "", "only messages this session id sent or is addressed by (session:<id>)")
	cmd.Flags().StringVar(&story, "story", "", "only messages on this story")
	return cmd
}

// storyMessageMarkCommand is `satelle story message-mark <id> <msg_id> --state ...`.
func storyMessageMarkCommand() *cobra.Command {
	var state, answer string
	cmd := &cobra.Command{
		Use:   "message-mark <id> <msg_id>",
		Short: "Record a message's state: delivered, answered or undeliverable",
		Long: `Append a message_mark row that moves a message to a state. The latest mark wins;
a message with no mark is waiting. Marks are bookkeeping, never evidence: they
do not make a reviewer's earlier verdict stale.

--state is delivered, answered or undeliverable. --answer <msg_id> names the
reply that answered it and goes only with answered. An unknown message or an
invalid state is refused. Replying with satelle story message --reply-to marks
the original answered for you.`,
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"id": args[0], "message": args[1], "state": state}
			if answer != "" {
				req["answer_id"] = answer
			}
			if by := config.ResolveSession(); by != "" {
				req["by_session_id"] = by
			}
			return dispatch(cmd, "story-message-mark", req)
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "delivered | answered | undeliverable (required)")
	cmd.Flags().StringVar(&answer, "answer", "", "msg_ id of the reply that answered it (with --state answered)")
	_ = cmd.MarkFlagRequired("state")
	return cmd
}

// senderIdentity is the sending session's id, name and location, each empty
// when it cannot be resolved: a message is never refused for want of one.
func senderIdentity() (id, name, location string) {
	root := initRepoRoot("")
	id = config.ResolveSession()
	name = currentSessionName(root)
	if loc, err := hosted.LocationID(root); err == nil {
		location = loc
	}
	return id, name, location
}
