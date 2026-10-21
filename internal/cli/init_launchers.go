package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentinstall"
	"github.com/bobmcallan/satelle/internal/config"
)

// healLaunchers is init's launcher step, beside its hook heal: an installed,
// satelle-owned launcher whose body differs from agentinstall.Content is
// rewritten (so an upgrade reaches launchers installed by an older binary), an
// unmarked file is left alone, and a launcher never installed is not created.
// For a harness that can name its sessions and has no launcher installed, it
// prints the hint for getting one.
func healLaunchers(out io.Writer) error {
	home := config.GlobalDir()
	rs, err := agentinstall.Heal(home)
	if err != nil {
		return err
	}
	for _, r := range rs {
		switch r.Action {
		case "updated":
			fmt.Fprintf(out, "  ~ %s launcher → %s (rewritten to the current form)\n", r.Name, r.Path)
		case "skipped":
			fmt.Fprintf(out, "  · %s launcher → %s (%s)\n", r.Name, r.Path, r.Note)
		}
	}
	for _, h := range agentcli.NameableHarnesses() {
		if _, serr := os.Stat(agentinstall.LauncherPath(home, h)); serr == nil {
			continue
		}
		fmt.Fprintf(out, "  · session naming: run `satelle agents install %s`, then %s\n", h, agentinstall.UsageNote(home, h))
	}
	return nil
}
