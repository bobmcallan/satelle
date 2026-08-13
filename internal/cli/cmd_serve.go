package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/serve"
)

func init() {
	var addr string
	var port int
	var noWatch bool
	var basePath string

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run satelled in the foreground (push-fed mirror UI)",
		Long: `serve runs satelled in the foreground — a single-process read-only web UI fed by CLI push (epic:serve-split).

It never opens per-repo runtime databases (~/.satelle/<repo-key>/satelle.db).
State arrives via POST /ingest/change and POST /ingest/snapshot. Partitions are
keyed by repo_key (decision-local-db-placement).

Deprecated as a long-term entrypoint (sty_80233c10): prefer the dedicated
satelled binary (systemd unit via satelle service install). This alias
keeps existing units and muscle memory working.`,
		// No needsStore: must not open a repo runtime DB (sty_dbdadfa0 AC1).
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = noWatch
			_ = basePath
			fmt.Fprintln(os.Stderr, "satelle serve is deprecated; the service now runs the dedicated satelled binary — re-run `satelle service install` to migrate")
			return serve.Run(cmd.Context(), serve.Options{Addr: addr, Port: port})
		},
	}
	serveCmd.Flags().StringVar(&addr, "addr", "", "bind address (default from global [service] addr)")
	serveCmd.Flags().IntVar(&port, "port", 0, "listen port (default from global service config or 8787)")
	serveCmd.Flags().BoolVar(&noWatch, "no-watch", false, "ignored (no maintenance loops remain; sty_dbdadfa0)")
	serveCmd.Flags().StringVar(&basePath, "base-path", "", "ignored (no supervised children; sty_dbdadfa0)")
	_ = serveCmd.Flags().MarkHidden("base-path")
	_ = serveCmd.Flags().MarkHidden("no-watch")
	register(serveCmd)
}
