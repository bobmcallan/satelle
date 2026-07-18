package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/logfile"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/web"
)

func init() {
	var addr string
	var port int
	var noWatch bool
	var basePath string

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the local read-only web server (push-fed mirror UI)",
		Long: `serve runs a single-process read-only web UI fed by CLI push (epic:serve-split).

It never opens per-repo runtime databases (~/.satelle/<repo-key>/satelle.db).
State arrives via POST /ingest/change (order:2 publisher) and POST /ingest/snapshot
(order:4 reconcile). Partitions are keyed by repo_key (decision-local-db-placement).

The CLI is the sole writer; this process is view-only plus ingest. Configure
[server] endpoint in satelle.toml so the CLI can reach this server. Press Ctrl-C
to stop.`,
		// No needsStore: must not open a repo runtime DB (sty_dbdadfa0 AC1).
		RunE: func(cmd *cobra.Command, args []string) error {
			if port == 0 {
				if gc, err := config.LoadGlobal(); err == nil && gc.Service.Port > 0 {
					port = gc.Service.Port
				} else {
					port = config.DefaultWebPort
				}
			}
			listenAddr := fmt.Sprintf("%s:%d", addr, port)

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			mirrorPath := mirror.DefaultPath(config.GlobalDir())
			ms, err := mirror.Open(mirrorPath)
			if err != nil {
				return fmt.Errorf("open mirror store: %w", err)
			}
			defer ms.Close()

			webSrv := web.NewMirror(ms)
			_ = noWatch
			_ = basePath

			serverLog := filepath.Join(config.GlobalDir(), mirror.DefaultDirName, "server.log")
			logCfg := logfile.Config{MaxSizeBytes: config.DefaultLogsMaxSizeKB * 1024, MaxFiles: config.DefaultLogsMaxFiles}
			banner := fmt.Sprintf("satelle serve (push-fed mirror) http://%s  mirror=%s  (Ctrl-C to stop)", listenAddr, mirrorPath)
			fmt.Fprintln(cmd.OutOrStdout(), banner)
			return listenServe(cmd, ctx, listenAddr, web.RequestLog(webSrv.Handler, serverLog, logCfg), "")
		},
	}
	serve.Flags().StringVar(&addr, "addr", "127.0.0.1", "bind address")
	serve.Flags().IntVar(&port, "port", 0, "listen port (default from global service config or 8787)")
	serve.Flags().BoolVar(&noWatch, "no-watch", false, "ignored (no maintenance loops remain; sty_dbdadfa0)")
	serve.Flags().StringVar(&basePath, "base-path", "", "ignored (no supervised children; sty_dbdadfa0)")
	_ = serve.Flags().MarkHidden("base-path")
	register(serve)
}

// listenServe runs an HTTP server on addr with handler until ctx is cancelled.
func listenServe(cmd *cobra.Command, ctx context.Context, addr string, handler http.Handler, banner string) error {
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if banner != "" {
		fmt.Fprintln(cmd.OutOrStdout(), banner)
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
