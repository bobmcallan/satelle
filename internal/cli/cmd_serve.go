package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/web"
)

func init() {
	var addr string
	var port int
	var noWatch bool

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web server (project page) for this repo",
		Long: `serve starts the local web server rendering the repo's project page from
the local database via the same verbs the CLI uses. It also runs the directory
monitor continuously so the authored-doc index stays fresh while serving.
Press Ctrl-C to stop.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			if port == 0 {
				port = a.Config.ResolveWebPort()
			}
			listenAddr := fmt.Sprintf("%s:%d", addr, port)

			// Signal-cancellable context shared by the watcher and the server.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Directory monitor: keep the index fresh while serving.
			if !noWatch {
				go func() {
					_ = a.Store.DocIndex.Watch(ctx, a.AuthoredDirs(), 2*time.Second,
						func(res docindex.SyncResult, err error) {
							if err != nil {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: %v\n", err)
							} else if res.Indexed > 0 || res.Pruned > 0 {
								fmt.Fprintf(cmd.ErrOrStderr(), "index: +%d -%d\n", res.Indexed, res.Pruned)
							}
						})
				}()
			}

			srv := &http.Server{Addr: listenAddr, Handler: web.Build(a)}
			// Shut the server down when the context is cancelled (Ctrl-C).
			go func() {
				<-ctx.Done()
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutCtx)
			}()

			fmt.Fprintf(cmd.OutOrStdout(), "satelle serving http://%s  (Ctrl-C to stop)\n", listenAddr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	serve.Flags().StringVar(&addr, "addr", "127.0.0.1", "bind address")
	serve.Flags().IntVar(&port, "port", 0, "listen port (default from config)")
	serve.Flags().BoolVar(&noWatch, "no-watch", false, "disable the directory monitor while serving")
	register(serve)
}
