package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/spf13/cobra"
)

// agentProfilesCmd is `satelle agent profiles` — list, and personal backup
// subcommands push/restore for the machine-wide catalog (sty_940938e3).
func agentProfilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "List or back up the machine-wide agent profile catalog (~/.satelle/agents.toml)",
		Long: `List the machine-wide agent profile catalog, or push/restore it to the
personal user store (authenticated, not project sync).

The catalog at ~/.satelle/agents.toml is OPERATOR runtime — execution profiles
shared across repos. Project sync never includes it. Personal backup uses
PUT/GET /api/v1/me/files/agents.toml on the hosted server (requires satelle login).

[vars] is NEVER uploaded: it is this machine's secret KV. After restore, re-enter
secrets under [vars] locally (or in satelle.local.toml). Restore refuses to
overwrite an existing catalog unless --force is passed (a .bak copy is kept).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			global, err := config.LoadGlobalAgents()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(global.Profiles) == 0 && len(global.Roles) == 0 {
				fmt.Fprintf(out, "no profiles defined — %s is absent or empty\n", config.GlobalAgentsPath())
				fmt.Fprintln(out, "run `satelle agent migrate` to seed a starter catalog from the selected agent CLI")
				return nil
			}
			printProfileCatalog(out, global)
			return nil
		},
	}
	cmd.AddCommand(agentProfilesPushCmd(), agentProfilesRestoreCmd())
	return cmd
}

func agentProfilesPushCmd() *cobra.Command {
	var serverFlag string
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Upload the catalog to the personal user store (excludes [vars])",
		Long: `Read ~/.satelle/agents.toml, strip the [vars] section (secrets stay on this
machine), and PUT the redacted body to the personal user store at path
agents.toml. Requires satelle login. Does not write into any repository.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfilesPush(cmd, serverFlag)
		},
	}
	cmd.Flags().StringVar(&serverFlag, "server", "", "Hosted server URL (overrides configured server)")
	return cmd
}

func agentProfilesRestoreCmd() *cobra.Command {
	var serverFlag string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Download the catalog from the personal user store onto this machine",
		Long: `GET the personal user-store catalog (agents.toml) and write it to
~/.satelle/agents.toml. Requires satelle login.

Non-destructive by default: if the catalog already exists, restore refuses and
names --force. With --force, the existing file is copied to agents.toml.bak
before overwrite. [vars] was not uploaded — re-enter secrets after restore.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfilesRestore(cmd, serverFlag, force)
		},
	}
	cmd.Flags().StringVar(&serverFlag, "server", "", "Hosted server URL (overrides configured server)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing catalog (keeps agents.toml.bak)")
	return cmd
}

func runProfilesPush(cmd *cobra.Command, serverFlag string) error {
	client, err := profilesHostedClient(serverFlag)
	if err != nil {
		return err
	}
	path := config.GlobalAgentsPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no catalog at %s — seed one with `satelle agent migrate` first", path)
		}
		return err
	}
	body := config.RedactVars(raw)
	res, err := client.PushMeFile(cmd.Context(), hosted.MeCatalogPath, body)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return fmt.Errorf("%w — run \"satelle login\" first", hosted.ErrLoginRequired)
		}
		return err
	}
	out := cmd.OutOrStdout()
	if res.Created {
		fmt.Fprintf(out, "pushed %s → personal store agents.toml (created)\n", path)
	} else {
		fmt.Fprintf(out, "pushed %s → personal store agents.toml (updated)\n", path)
	}
	fmt.Fprintln(out, "[vars] was not uploaded — re-enter secrets on the machine that restores")
	return nil
}

func runProfilesRestore(cmd *cobra.Command, serverFlag string, force bool) error {
	client, err := profilesHostedClient(serverFlag)
	if err != nil {
		return err
	}
	// Network first — never touch the filesystem on auth failure.
	body, _, err := client.MeFileContent(cmd.Context(), hosted.MeCatalogPath)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return fmt.Errorf("%w — run \"satelle login\" first", hosted.ErrLoginRequired)
		}
		return err
	}
	path := config.GlobalAgentsPath()
	if st, serr := os.Stat(path); serr == nil && !st.IsDir() {
		if !force {
			return fmt.Errorf("refusing to overwrite %s — pass --force to replace (a .bak copy is kept)", path)
		}
		bak := path + ".bak"
		prev, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("read existing catalog for .bak: %w", rerr)
		}
		if werr := os.WriteFile(bak, prev, 0o600); werr != nil {
			return fmt.Errorf("write %s: %w", bak, werr)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "kept previous catalog at %s\n", bak)
	} else if serr != nil && !os.IsNotExist(serr) {
		return serr
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "restored personal store agents.toml → %s\n", path)
	fmt.Fprintln(cmd.OutOrStdout(), "re-enter [vars] secrets locally if profiles reference ${NAME}")
	return nil
}

func profilesHostedClient(serverFlag string) (*hosted.Client, error) {
	server := resolveServer(serverFlag)
	if server == "" {
		return nil, fmt.Errorf("%w — run \"satelle login\" first (no hosted server configured)", hosted.ErrLoginRequired)
	}
	return hosted.NewClient(server, hosted.FileStore{}, nil), nil
}
