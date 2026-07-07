package cli

// `satelle login` / `logout` / `whoami` — the client half of the hosted-server
// auth (sty_2fc93374). login runs an OAuth 2.1 + PKCE loopback flow, persists the
// tokens to the per-user credential store, records the server in the GLOBAL config
// (~/.satelle/config.toml) so one sign-in serves every repo (sty_53ccf845), and
// prints the signed-in identity. None of these commands touch the local DB, so
// they carry no store annotation and work in a fresh clone.

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
		serverArg string
		noBrowser bool
		timeout   time.Duration
	)

	login := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to the hosted satelle-server (OAuth 2.1 + PKCE)",
		Long: `login runs a browser-based OAuth 2.1 + PKCE flow against the hosted
satelle-server, stores the access + refresh tokens in the per-user credential
store ($XDG_CONFIG_HOME/satelle/credentials.toml), records the server URL in the
machine-wide global config (~/.satelle/config.toml) so one sign-in serves every
repo, and prints your identity.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(cmd, serverArg, noBrowser, timeout)
		},
	}
	login.Flags().StringVar(&serverArg, "server", "", "Hosted server URL (recorded in the global config; overrides the configured server).")
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
	logout.Flags().StringVar(&logoutServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	register(logout)

	var whoamiServer string
	whoami := &cobra.Command{
		Use:   "whoami",
		Short: "Print the hosted-server principal (GET /api/v1/me)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhoami(cmd, whoamiServer)
		},
	}
	whoami.Flags().StringVar(&whoamiServer, "server", "", "Hosted server URL (overrides the configured global/repo server).")
	register(whoami)
}

// resolveServer picks the server: the flag wins, else the GLOBAL config's
// [hosted] server (the machine-wide binding), else the committed repo config's
// [hosted] server (the read-only backward-compat fallback). A blank result is an
// error at the caller.
func resolveServer(flagServer string) string {
	cfg, _, err := config.Load("")
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		// A malformed repo config still lets --server (or global) drive login.
		cfg = config.Config{}
	}
	if s := strings.TrimSpace(flagServer); s != "" {
		return strings.TrimRight(s, "/")
	}
	// Global-first, with the repo config as the read-only fallback.
	return config.ResolveHostedServer(cfg)
}

// recordLoginBinding persists the post-authentication binding: the server goes to
// the GLOBAL config (one sign-in serves every repo). Tokens are handled
// separately by the credential store. Factored out so the persistence is
// unit-testable without the browser OAuth flow.
func recordLoginBinding(server string) error {
	if err := config.SaveGlobalHostedServer(server); err != nil {
		return fmt.Errorf("record hosted server in the global config: %w", err)
	}
	return nil
}

func runLogin(cmd *cobra.Command, serverArg string, noBrowser bool, timeout time.Duration) error {
	server := resolveServer(serverArg)
	if server == "" {
		return fmt.Errorf("no hosted server configured — pass --server <url> (it is recorded globally for every repo)")
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
	// Record the binding: server → global config (one sign-in serves every repo).
	// Tokens stay in the per-user store.
	if err := recordLoginBinding(server); err != nil {
		return err
	}

	// Fetch and print the signed-in identity, and persist it into the credential
	// so the web UI resolves identity locally with no render-time fetch
	// (sty_467c6944). A failed identity fetch does not fail the login.
	who, err := hosted.NewClient(server, store, nil).Me(cmd.Context())
	if err != nil {
		fmt.Fprintf(out, "Signed in to %s (could not fetch identity: %v)\n", server, err)
		return nil
	}
	if fresh, lErr := store.Load(server); lErr == nil {
		fresh.DisplayName, fresh.Email = who.DisplayName, who.Email
		_ = store.Save(fresh)
	}
	printPrincipal(cmd, server, who)
	return nil
}

func runLogout(cmd *cobra.Command, serverArg string) error {
	server := resolveServer(serverArg)
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
	server := resolveServer(serverArg)
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
