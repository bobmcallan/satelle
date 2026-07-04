package cli

// `satelle login` / `logout` / `whoami` — the client half of the hosted-server
// auth (sty_2fc93374). login runs an OAuth 2.1 + PKCE loopback flow, persists
// the tokens to the per-user credential store, records server+project in the
// committed satelle.toml, and prints the signed-in identity. None of these
// commands touch the local DB, so they carry no store annotation and work in a
// fresh clone.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

func init() {
	var (
		serverArg  string
		projectArg string
		noBrowser  bool
		timeout    time.Duration
	)

	login := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to the hosted satelle-server (OAuth 2.1 + PKCE)",
		Long: `login runs a browser-based OAuth 2.1 + PKCE flow against the hosted
satelle-server, stores the access + refresh tokens in the per-user credential
store ($XDG_CONFIG_HOME/satelle/credentials.toml), records the server URL and
project slug in the committed .satelle/satelle.toml, and prints your identity.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(cmd, serverArg, projectArg, noBrowser, timeout)
		},
	}
	login.Flags().StringVar(&serverArg, "server", "", "Hosted server URL (overrides [hosted] server in satelle.toml).")
	login.Flags().StringVar(&projectArg, "project", "", "Hosted project slug to record in satelle.toml.")
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the authorize URL instead of opening a browser.")
	login.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "How long to wait for the browser approval.")
	register(login)

	var logoutServer string
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Clear stored hosted-server credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogout(cmd, logoutServer)
		},
	}
	logout.Flags().StringVar(&logoutServer, "server", "", "Hosted server URL (overrides [hosted] server in satelle.toml).")
	register(logout)

	var whoamiServer string
	whoami := &cobra.Command{
		Use:   "whoami",
		Short: "Print the hosted-server principal (GET /api/v1/me)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhoami(cmd, whoamiServer)
		},
	}
	whoami.Flags().StringVar(&whoamiServer, "server", "", "Hosted server URL (overrides [hosted] server in satelle.toml).")
	register(whoami)
}

// resolveServer picks the server from the flag, else the committed config's
// [hosted] server. Returns the resolved server and the committed config path
// (for writing server/project back). A blank result is an error at the caller.
func resolveServer(flagServer string) (server, cfgPath string) {
	cfg, path, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		// A malformed config still lets --server drive login; surface nothing here.
		path = ""
	}
	server = strings.TrimSpace(flagServer)
	if server == "" {
		server = cfg.Hosted.Server
	}
	return strings.TrimRight(strings.TrimSpace(server), "/"), path
}

func runLogin(cmd *cobra.Command, serverArg, projectArg string, noBrowser bool, timeout time.Duration) error {
	server, cfgPath := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — pass --server <url> (or set [hosted] server in satelle.toml)")
	}
	out := cmd.OutOrStdout()

	opts := hosted.LoginOptions{Timeout: timeout, Out: out}
	if !noBrowser {
		opts.OpenBrowser = hosted.OpenBrowser
	}

	cred, err := hosted.Login(cmd.Context(), nil, server, opts)
	if err != nil {
		return err
	}

	store := hosted.FileStore{}
	if err := store.Save(cred); err != nil {
		return err
	}
	// Record server + project in the committed config (secret-free; tokens stay
	// in the per-user store).
	if err := config.SaveHostedServer(cfgPath, server, projectArg); err != nil {
		return fmt.Errorf("record hosted server in satelle.toml: %w", err)
	}

	// Print the signed-in identity.
	who, err := hosted.NewClient(server, store, nil).Me(cmd.Context())
	if err != nil {
		fmt.Fprintf(out, "Signed in to %s (could not fetch identity: %v)\n", server, err)
		return nil
	}
	printPrincipal(cmd, server, who)
	return nil
}

func runLogout(cmd *cobra.Command, serverArg string) error {
	server, _ := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — pass --server <url>")
	}
	if err := (hosted.FileStore{}).Delete(server); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s.\n", server)
	return nil
}

func runWhoami(cmd *cobra.Command, serverArg string) error {
	server, _ := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — pass --server <url>")
	}
	who, err := hosted.NewClient(server, hosted.FileStore{}, nil).Me(cmd.Context())
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return err
		}
		return fmt.Errorf("whoami: %w", err)
	}
	printPrincipal(cmd, server, who)
	return nil
}

func printPrincipal(cmd *cobra.Command, server string, p hosted.Principal) {
	name := p.DisplayName
	if name == "" {
		name = p.Email
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s as %s <%s> (role %s)\n", server, name, p.Email, p.Role)
}
