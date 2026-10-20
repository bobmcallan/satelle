// `satelle secret` manages the secrets a remote's agent may pull on demand.
// Secrets are configuration kept in .satelle/config/.env; push uploads each key
// to satelle-server, which holds the value and shows it to no reader. grant and
// revoke decide which remote may read a key. get runs on a remote node, under its
// agent, and asks the node for one key over the sealed channel to local satelle.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
)

// secretHostedClient is the signed-in client the secret verbs use; a test
// replaces it.
var secretHostedClient = func(server string) *hosted.Client {
	return hosted.NewClient(server, hosted.DefaultStore(), nil)
}

// secretGetTimeout bounds `satelle secret get`.
const secretGetTimeout = 2 * time.Minute

func init() {
	secret := &cobra.Command{
		Use:   "secret",
		Short: "Upload secrets to satelle-server, grant a remote read of one, and read one on a remote node",
		Long: `Secrets are configuration, kept in .satelle/config/.env and never synced or
published. push uploads each key to satelle-server, which holds the value and
returns it to no API, tool or page — list shows names and attributes only. grant
lets one remote's agent read a key; the remote's definition must also name the
key under secrets. An agent on the node asks for a key when a step needs it
(satelle secret get) and receives it for that run only: nothing is pre-loaded
onto the node or written to it.`,
	}

	var server string
	serverFlag := func(c *cobra.Command) {
		c.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server)")
	}

	var pushPersonal bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Upload every key in .satelle/config/.env to satelle-server",
		Long: `Reads .satelle/config/.env and uploads each key with its value, which the server
holds hidden. --personal marks every key pushed as personal: readable only by
your own remotes that no one else can fire (use it for a subscription token).
Prints each key pushed, never a value.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			vals, err := config.ReadDotEnv(remoteDataDir())
			if err != nil {
				return err
			}
			if len(vals) == 0 {
				return fmt.Errorf("no keys to push: %s is missing or empty", config.DefaultDataDir+"/"+config.DotEnvFile)
			}
			client, err := secretClient(server)
			if err != nil {
				return err
			}
			keys := make([]string, 0, len(vals))
			for k := range vals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if err := client.PutSecret(cmd.Context(), k, vals[k], pushPersonal); err != nil {
					return fmt.Errorf("push %s: %w", k, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "pushed %s\n", k)
			}
			return nil
		},
	}
	push.Flags().BoolVar(&pushPersonal, "personal", false, "mark every key pushed as personal (readable only by your own remotes that no one else can fire)")
	serverFlag(push)

	list := &cobra.Command{
		Use:   "list",
		Short: "List the secrets on satelle-server: names and attributes, never a value",
		Long: `Lists each secret on satelle-server with whether it holds a value, whether it is
personal and which remotes may read it. The server returns no value to anyone,
so none can be shown here.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := secretClient(server)
			if err != nil {
				return err
			}
			metas, err := client.ListSecrets(cmd.Context())
			if err != nil {
				return err
			}
			printSecrets(cmd.OutOrStdout(), metas)
			return nil
		},
	}
	serverFlag(list)

	var grantRemote string
	var grantPersonal bool
	grant := &cobra.Command{
		Use:   "grant <KEY> --remote <name>",
		Short: "Let one remote's agent read a key",
		Long: `Sets the remote's read of KEY on satelle-server. The remote's definition must
also list KEY under secrets, or local satelle refuses the agent's request.
--personal also marks the key personal: only your own remotes that no one else
can fire may read it; the server enforces that.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkSecretArgs(args[0], grantRemote); err != nil {
				return err
			}
			client, err := secretClient(server)
			if err != nil {
				return err
			}
			if err := client.GrantSecret(cmd.Context(), args[0], grantRemote, grantPersonal); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "granted %s to remote %s\n", args[0], grantRemote)
			return nil
		},
	}
	grant.Flags().StringVar(&grantRemote, "remote", "", "the remote that may read the key")
	grant.Flags().BoolVar(&grantPersonal, "personal", false, "also mark the key personal (readable only by your own remotes that no one else can fire)")
	_ = grant.MarkFlagRequired("remote")
	serverFlag(grant)

	var revokeRemote string
	revoke := &cobra.Command{
		Use:   "revoke <KEY> --remote <name>",
		Short: "Stop a remote's agent reading a key",
		Long: `Removes the remote's read of KEY on satelle-server. Nothing is cached on a node,
so the agent's next request for the key is refused.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkSecretArgs(args[0], revokeRemote); err != nil {
				return err
			}
			client, err := secretClient(server)
			if err != nil {
				return err
			}
			if err := client.RevokeSecret(cmd.Context(), args[0], revokeRemote); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s from remote %s\n", args[0], revokeRemote)
			return nil
		},
	}
	revoke.Flags().StringVar(&revokeRemote, "remote", "", "the remote that no longer reads the key")
	_ = revoke.MarkFlagRequired("remote")
	serverFlag(revoke)

	var socket, job string
	get := &cobra.Command{
		Use:   "get <KEY>",
		Short: "On a remote node: print one secret's value, for this run only",
		Long: `Runs on a remote node, under its agent. Asks the node for KEY; the node asks local
satelle over the sealed channel; local satelle fetches the value from
satelle-server if the remote's definition lists the key and the remote holds a
grant on it, and sends it back sealed. The value is printed to stdout and kept
nowhere. A key the remote may not read exits non-zero with the reason.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := socket
			if path == "" {
				path = remotenode.SocketPath(remoteDataDir())
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), secretGetTimeout)
			defer cancel()
			v, err := remotenode.SocketGet(ctx, path, args[0], job)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), v)
			return err
		},
	}
	get.Flags().StringVar(&socket, "socket", "", "the node's socket (default: under this repo's data dir)")
	get.Flags().StringVar(&job, "job", "", "the job to read for, when the node runs several")

	secret.AddCommand(push, list, grant, revoke, get)
	register(secret)
}

// secretClient is the client for the configured (or flagged) hosted server.
func secretClient(serverFlag string) (*hosted.Client, error) {
	srv := resolveServer(serverFlag)
	if srv == "" {
		return nil, errors.New("no hosted server configured — run \"satelle login\"")
	}
	return secretHostedClient(srv), nil
}

func checkSecretArgs(key, remote string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("name the secret key")
	}
	if !remotedef.ValidName(remote) {
		return fmt.Errorf("remote name %q must be lower-case letters, digits, - or _, starting with a letter or digit", remote)
	}
	return nil
}

// printSecrets renders the server's secret listing: a name, whether it holds a
// value, whether it is personal and who may read it. There is no value to show.
func printSecrets(out io.Writer, metas []hosted.SecretMeta) {
	if len(metas) == 0 {
		fmt.Fprintln(out, "no secrets on the server — satelle secret push uploads .satelle/config/.env")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tHAS_VALUE\tPERSONAL\tREADERS")
	for _, m := range metas {
		readers := "-"
		if len(m.Readers) > 0 {
			readers = strings.Join(m.Readers, ",")
		}
		fmt.Fprintf(tw, "%s\t%t\t%t\t%s\n", m.Key, m.HasValue, m.Personal, readers)
	}
	_ = tw.Flush()
}
