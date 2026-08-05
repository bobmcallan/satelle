package cli

// `satelle login` / `logout` / `whoami` — the client half of the hosted-server
// auth (sty_2fc93374). login runs an OAuth 2.1 + PKCE loopback flow, persists the
// tokens to the per-user credential store, records the server in the GLOBAL config
// (~/.satelle/config.toml) so one sign-in serves every repo (sty_53ccf845), and
// prints the signed-in identity. None of these commands touch the local DB, so
// they carry no store annotation and work in a fresh clone.
//
// --workspace (epic:scoped-sync, order:4) records the user's ACTIVE hosted
// workspace for scoped sync. The default login writes nothing — the personal
// workspace is the resolver's zero-config default; --workspace <name> selects a
// team workspace and records the choice in the gitignored satelle.local.toml
// overlay (a per-developer value, never the team-committed satelle.toml).

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

func init() {
	var (
		serverArg    string
		workspaceArg string
		noBrowser    bool
		timeout      time.Duration
	)

	login := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to the hosted satelle-server (OAuth 2.1 + PKCE)",
		Long: `login runs a browser-based OAuth 2.1 + PKCE flow against the hosted
satelle-server, stores the access + refresh tokens in the per-user credential
store ($XDG_CONFIG_HOME/satelle/credentials.toml), records the server URL in the
machine-wide global config (~/.satelle/config.toml) so one sign-in serves every
repo, and prints your identity.

--workspace <name> records your active workspace for scoped sync (personal by
default; a team-workspace name selects it). The selection is written to the
gitignored per-user .satelle/satelle.local.toml, not the team-committed
satelle.toml.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(cmd, serverArg, workspaceArg, noBrowser, timeout)
		},
	}
	login.Flags().StringVar(&serverArg, "server", "", "Hosted server URL (recorded in the global config; overrides the configured server).")
	login.Flags().StringVar(&workspaceArg, "workspace", "", "Record the active workspace (a team-workspace name, or id) for scoped sync; default is personal.")
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the authorize URL instead of opening a browser.")
	login.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "How long to wait for the browser approval.")
	register(login)

	var logoutServer string
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Clear stored hosted-server credentials",
		Long: `Clear the stored hosted-server credentials on this machine.

Local work is unaffected — satelle runs fully locally, and only the hosted sync
and project commands need a session. Nothing already synced is withdrawn: this
forgets a credential, it does not revoke or delete anything server-side.`,
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
		Long: `Print who the stored credential authenticates as on the hosted server.

Reach for it when a hosted command is refused: it separates "not logged in" from
"logged in as the wrong principal", which the refusal alone often does not. It
asks the server, so it needs the network.`,
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

// repoConfigPath returns the committed .satelle/satelle.toml path for the CWD (""
// outside a repo). The active-workspace selection is recorded in the per-user
// satelle.local.toml that sits BESIDE this path; an empty result means the login
// is out-of-repo and records nothing.
func repoConfigPath() string {
	_, path, err := config.Load("")
	if err != nil {
		return ""
	}
	return path
}

// recordActiveWorkspace writes the active-workspace selection to the gitignored
// satelle.local.toml overlay beside the committed config — NOT the team
// satelle.toml — because the active workspace is a per-developer choice (the
// personal default, or a team workspace the developer elects). A committed
// [hosted] workspace in satelle.toml stays a hand-authored TEAM DEFAULT the
// overlay overrides per-key. An empty cfgPath (out-of-repo login) records
// nothing, preserving fresh-clone portability. Factored out so the write is
// unit-testable without the OAuth flow.
func recordActiveWorkspace(cfgPath, name string) error {
	if strings.TrimSpace(cfgPath) == "" {
		return nil // out-of-repo login authenticates without recording
	}
	localPath := filepath.Join(filepath.Dir(cfgPath), config.LocalConfigName)
	edit := config.KeyEdit{Section: "hosted", Key: "workspace", Value: strconv.Quote(name)}
	if err := config.SaveConfigValues(localPath, []config.KeyEdit{edit}); err != nil {
		return fmt.Errorf("record active workspace: %w", err)
	}
	return nil
}

// matchWorkspace resolves a --workspace argument against the fetched list: an
// exact NAME match wins, then an exact ID match (the unambiguous fallback). The
// server returns no slug, so name is the primary handle a user types.
func matchWorkspace(workspaces []hosted.Workspace, arg string) (hosted.Workspace, bool) {
	for _, ws := range workspaces {
		if ws.Name == arg {
			return ws, true
		}
	}
	for _, ws := range workspaces {
		if ws.ID == arg {
			return ws, true
		}
	}
	return hosted.Workspace{}, false
}

// workspaceNotFound builds the clear, human error for an unresolvable
// --workspace argument, listing the available workspaces as "name (kind)" pairs.
// It is built from the parsed list, never the raw server body, so a response
// detail cannot leak (AC4).
func workspaceNotFound(arg string, workspaces []hosted.Workspace) error {
	if len(workspaces) == 0 {
		return fmt.Errorf("workspace %q not found — the account has no workspaces", arg)
	}
	parts := make([]string, len(workspaces))
	for i, ws := range workspaces {
		parts[i] = fmt.Sprintf("%s (%s)", ws.Name, ws.Kind)
	}
	return fmt.Errorf("workspace %q not found — available: %s (re-run login with one of these, or drop --workspace for your personal workspace)", arg, strings.Join(parts, ", "))
}

func runLogin(cmd *cobra.Command, serverArg, workspaceArg string, noBrowser bool, timeout time.Duration) error {
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

	client := hosted.NewClient(server, store, nil)

	// Resolve the active workspace (--workspace). Done AFTER auth so a fetch
	// failure or an unknown name surfaces AFTER tokens+server are saved, as a
	// clear retryable error — never a raw body (AC4). Default login (no flag)
	// writes nothing: personal is the resolver's zero-config default.
	activeWorkspace := ""
	if ws := strings.TrimSpace(workspaceArg); ws != "" {
		workspaces, werr := client.Workspaces(cmd.Context())
		if werr != nil {
			return fmt.Errorf("resolve workspace %q: %w", ws, werr)
		}
		selected, ok := matchWorkspace(workspaces, ws)
		if !ok {
			return workspaceNotFound(ws, workspaces)
		}
		if cfgPath := repoConfigPath(); cfgPath != "" {
			if err := recordActiveWorkspace(cfgPath, selected.Name); err != nil {
				return err
			}
			activeWorkspace = selected.Name
			fmt.Fprintf(out, "Active workspace set to %s.\n", selected.Name)
		} else {
			// Out-of-repo: auth succeeded but there is no .satelle/satelle.toml
			// to record the selection in — re-run inside a repo to bind it.
			fmt.Fprintf(out, "Resolved workspace %s, but no .satelle/satelle.toml here to record it in — re-run inside a repo to bind it.\n", selected.Name)
		}
	}

	// Fetch and print the signed-in identity, and persist it into the credential
	// so the web UI resolves identity locally with no render-time fetch
	// (sty_467c6944). A failed identity fetch does not fail the login.
	who, err := client.Me(cmd.Context())
	if err != nil {
		if activeWorkspace != "" {
			fmt.Fprintf(out, "Signed in to %s (active workspace %s; could not fetch identity: %v)\n", server, activeWorkspace, err)
		} else {
			fmt.Fprintf(out, "Signed in to %s (could not fetch identity: %v)\n", server, err)
		}
		return nil
	}
	if fresh, lErr := store.Load(server); lErr == nil {
		fresh.DisplayName, fresh.Email = who.DisplayName, who.Email
		_ = store.Save(fresh)
	}
	printPrincipal(cmd, server, who, activeWorkspace)
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
	printPrincipal(cmd, server, who, resolveWhoamiWorkspace())
	return nil
}

// resolveWhoamiWorkspace loads the repo config (best-effort) and resolves the
// active workspace so whoami surfaces the scoped-sync destination alongside the
// identity. Empty when there is no repo config (out-of-repo whoami prints none).
func resolveWhoamiWorkspace() string {
	cfg, _, err := config.Load("")
	if err != nil {
		return ""
	}
	return cfg.ResolveActiveWorkspace().Name
}

func printPrincipal(cmd *cobra.Command, server string, p hosted.Principal, activeWorkspace string) {
	name := p.DisplayName
	if name == "" {
		name = p.Email
	}
	out := cmd.OutOrStdout()
	if activeWorkspace != "" {
		fmt.Fprintf(out, "Signed in to %s as %s <%s> (role %s, workspace %s)\n", server, name, p.Email, p.Role, activeWorkspace)
		return
	}
	fmt.Fprintf(out, "Signed in to %s as %s <%s> (role %s)\n", server, name, p.Email, p.Role)
}
