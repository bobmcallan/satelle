package cli

// `satelle settings` — read/write configuration in two explicit scopes (sty_e2fba595 /
// sty_21a7d16d):
//
//   satelle settings [key] [value]              # REPO scope (committed .satelle/satelle.toml) — DEFAULT
//   satelle settings                            #   no args → list every repo key + resolved value
//   satelle settings log_level warn             #   write a repo key (surgical upsert, comments preserved)
//   satelle settings --global                   # GLOBAL scope (~/.satelle/config.toml) — listen + hosted
//   satelle settings --global port 8787
//   satelle settings --global addr 127.0.0.1
//   satelle settings --global endpoint http://127.0.0.1:8787
//   satelle settings --global server <url>      # hosted server, no login
//   satelle settings --global server --clear    #   clear it
//   satelle settings server <url>               # DEPRECATED alias of `--global server` (still works, warns)
//
// Repo scope is the counterpart to the read-only web settings page (sty_e1740d82);
// both iterate the ONE shared schema config.Settings, so they never drift. Machine
// listen keys (port/addr/endpoint) live only on --global. A leftover
// `satelle settings web_port` fails with Stray.Warning().

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/spf13/cobra"
)

func init() { register(newSettingsCommand()) }

func newSettingsCommand() *cobra.Command {
	var global bool

	settings := &cobra.Command{
		Use:   "settings [key] [value]",
		Short: "Read or write repo settings (.satelle/satelle.toml); --global for the machine-wide config",
		Long: `settings reads or writes configuration in two scopes.

Repo (default) is committed .satelle/satelle.toml — list, print, or write one
key (surgical upsert; comments kept). substrate_roots is display-only.

--global is ~/.satelle/config.toml: port, addr, endpoint (satelled listen),
and hosted server (no login). A leftover repo web_port fails; re-home with
satelle migrate --yes.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettings(cmd, args, global)
		},
	}
	settings.PersistentFlags().BoolVar(&global, "global", false, "Operate on the machine-wide global config (~/.satelle/config.toml) instead of the repo config.")

	var clear bool
	server := &cobra.Command{
		Use:   "server [url]",
		Short: "Show, set, or clear the GLOBAL hosted-server URL (no login required)",
		Long: `server manages the machine-wide hosted-server binding in the global config,
without authenticating — configuring the server is separate from signing in.

  satelle settings --global server                 # print the current server
  satelle settings --global server <url>           # set it (then sign in via the UI or 'satelle login')
  satelle settings --global server --clear         # remove it

'satelle login --server <url>' also works — it sets the server AND authenticates.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Back-compat (AC2): `satelle settings server …` predates the --global flag
			// and used to mean the global server. It still works, with a nudge to the
			// scoped form.
			if !global {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: `satelle settings server` is deprecated — use `satelle settings --global server`")
			}
			return runSettingsServer(cmd, args, clear)
		},
	}
	server.Flags().BoolVar(&clear, "clear", false, "Remove the configured global hosted server.")
	settings.AddCommand(server)
	return settings
}

// runSettings dispatches the parent command: --global manages machine listen
// keys and the hosted server; otherwise it is repo scope (list / read / write).
func runSettings(cmd *cobra.Command, args []string, global bool) error {
	if global {
		return runGlobalSettings(cmd, args)
	}
	if len(args) > 0 && isStrayRepoKey(args[0]) {
		return strayRepoKeyError(args[0])
	}
	switch len(args) {
	case 0:
		return listRepoSettings(cmd)
	case 1:
		return readRepoSetting(cmd, args[0])
	default:
		return writeRepoSetting(cmd, args[0], args[1])
	}
}

func isStrayRepoKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "web_port", "server.endpoint", "endpoint":
		return true
	default:
		return false
	}
}

func strayRepoKeyError(key string) error {
	gc, _ := config.LoadGlobal()
	s := config.Stray{
		Key:            key,
		File:           config.ConfigName,
		RepoValue:      "(settings write refused)",
		EffectiveValue: strconv.Itoa(gc.Service.ResolvePort()),
	}
	if key != "web_port" {
		if e := gc.Service.ResolveEndpoint(); e != "" {
			s.EffectiveValue = e
		} else {
			s.EffectiveValue = "(disabled)"
		}
	}
	return fmt.Errorf("%s", s.Warning())
}

var globalListenKeys = []string{"port", "addr", "endpoint"}

func runGlobalSettings(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return listGlobalSettings(cmd)
	}
	key := strings.TrimSpace(args[0])
	if key == "server" {
		return runSettingsServer(cmd, args[1:], false)
	}
	if key == "web_port" {
		return strayRepoKeyError("web_port")
	}
	ok := false
	for _, k := range globalListenKeys {
		if k == key {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("unknown global setting %q — valid keys: %s, server", key, strings.Join(globalListenKeys, ", "))
	}
	if len(args) == 1 {
		return printGlobalListenKey(cmd, key)
	}
	return writeGlobalListenKey(cmd, key, args[1])
}

func listGlobalSettings(cmd *cobra.Command) error {
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# machine settings — %s\n", config.GlobalConfigPath())
	fmt.Fprintf(out, "port = %d\n", gc.Service.ResolvePort())
	fmt.Fprintf(out, "addr = %s\n", gc.Service.ResolveAddr())
	ep := gc.Service.ResolveEndpoint()
	if ep == "" {
		ep = "(disabled)"
	}
	fmt.Fprintf(out, "endpoint = %s\n", ep)
	if s := gc.Hosted.ResolveServer(); s != "" {
		fmt.Fprintf(out, "server = %s\n", s)
	} else {
		fmt.Fprintln(out, "server = (unset)")
	}
	return nil
}

func printGlobalListenKey(cmd *cobra.Command, key string) error {
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	switch key {
	case "port":
		fmt.Fprintln(cmd.OutOrStdout(), gc.Service.ResolvePort())
	case "addr":
		fmt.Fprintln(cmd.OutOrStdout(), gc.Service.ResolveAddr())
	case "endpoint":
		ep := gc.Service.ResolveEndpoint()
		if ep == "" {
			ep = "(disabled)"
		}
		fmt.Fprintln(cmd.OutOrStdout(), ep)
	}
	return nil
}

func writeGlobalListenKey(cmd *cobra.Command, key, value string) error {
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	switch key {
	case "port":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			return fmt.Errorf("port: must be a whole number")
		}
		gc.Service.Port = n
	case "addr":
		gc.Service.Addr = strings.TrimSpace(value)
	case "endpoint":
		gc.Service.Endpoint = strings.TrimSpace(value)
	}
	if err := config.SaveGlobal(gc); err != nil {
		return err
	}
	return printGlobalListenKey(cmd, key)
}

// listRepoSettings prints every repo key and its resolved value — the CLI
// counterpart to the read-only web settings page.
func listRepoSettings(cmd *cobra.Command) error {
	cfg, path, err := config.Load("")
	if err != nil {
		return repoConfigError(err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# repo settings — %s\n", path)
	for _, s := range config.Settings {
		v := oneLine(config.SettingDisplay(cfg, s))
		if v == "" {
			v = "(unset)"
		}
		fmt.Fprintf(out, "%s = %s\n", s.FieldID(), v)
	}
	return nil
}

func readRepoSetting(cmd *cobra.Command, key string) error {
	s, ok := config.SettingByID(key)
	if !ok {
		return unknownKeyError(key)
	}
	cfg, _, err := config.Load("")
	if err != nil {
		return repoConfigError(err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), config.SettingDisplay(cfg, s))
	return nil
}

func writeRepoSetting(cmd *cobra.Command, key, value string) error {
	s, ok := config.SettingByID(key)
	if !ok {
		return unknownKeyError(key)
	}
	rhs, err := s.EncodeValue(value)
	if err != nil {
		return err
	}
	_, path, err := config.Load("")
	if err != nil {
		return repoConfigError(err)
	}
	if err := config.SaveConfigValues(path, []config.KeyEdit{{Section: s.Section, Key: s.Key, Value: rhs}}); err != nil {
		return err
	}
	cfg, _, _ := config.Load(path) // read back for confirmation
	fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", s.FieldID(), oneLine(config.SettingDisplay(cfg, s)))
	return nil
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", ", ") }

func unknownKeyError(key string) error {
	return fmt.Errorf("unknown repo setting %q — valid keys: %s", key, strings.Join(config.SettingIDs(), ", "))
}

func repoConfigError(err error) error {
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("no .satelle/satelle.toml found — run inside a satelle repo (or run `satelle init`)")
	}
	return err
}

func runSettingsServer(cmd *cobra.Command, args []string, clear bool) error {
	out := cmd.OutOrStdout()

	if clear {
		if len(args) > 0 {
			return fmt.Errorf("--clear takes no url argument")
		}
		if err := config.ClearGlobalHostedServer(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Cleared the global hosted server.")
		return nil
	}

	if len(args) == 1 {
		url := strings.TrimSpace(args[0])
		if err := config.SaveGlobalHostedServer(url); err != nil {
			return err
		}
		gc, _ := config.LoadGlobal()
		fmt.Fprintf(out, "Global hosted server set to %s.\nSign in with the web UI (Sign in) or `satelle login`.\n", gc.Hosted.ResolveServer())
		return nil
	}

	// No arg → print the current value.
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	if s := gc.Hosted.ResolveServer(); s != "" {
		fmt.Fprintln(out, s)
	} else {
		fmt.Fprintln(out, "no global hosted server configured — set one with `satelle settings --global server <url>`")
	}
	return nil
}
