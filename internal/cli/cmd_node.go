// `satelle node join` is what runs on a machine acting as a remote node: it
// creates the node's data dir and store, fetches the definition it runs from
// satelle.dev with its node token, installs and reindexes its substrate, joins
// the relay session, fetches the declared repos read-only, reports ready and
// serves the controller's jobs. It reads its secrets and ends its run through
// satelle.dev with the same token. It is a node mode, not a developer command:
// remotes are defined, published, secreted and launched through satelle-server,
// whose launcher builds this argv.
package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Seams a test replaces; production leaves them alone.
var (
	// nodeChannelOptions configure the node's end of the channel.
	nodeChannelOptions []remotechan.Option
	// nodeIdentity makes the node's key.
	nodeIdentity = remotechan.NewIdentity
	// nodeServer is the satelle.dev the node asks for its definition and secrets.
	nodeServer = func() string { return config.ResolveHostedServer(config.Config{}) }
	// nodeHTTP is the HTTP client for those calls; nil is a 30 second default.
	nodeHTTP *http.Client
	// nodeFetchEnv is added to git's environment when the node fetches its repos.
	nodeFetchEnv []string
)

// nodeEndTimeout bounds the one end call a stopping node makes.
const nodeEndTimeout = 10 * time.Second

func init() {
	node := &cobra.Command{
		Use:    "node",
		Short:  "Run this machine as a remote node",
		Hidden: true,
	}
	var root, relay, session, token string
	var tokenStdin bool
	join := &cobra.Command{
		Use:    "join",
		Short:  "Run on a remote node: set up from the definition satelle.dev serves and serve its jobs",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read before the root is resolved or initialised, and before the
			// end call is deferred, so a bad token leaves the node untouched.
			if tokenStdin {
				t, err := readTokenStdin(cmd.InOrStdin())
				if err != nil {
					return err
				}
				token = t
			}
			return runNodeJoin(cmd, root, relay, session, token)
		},
	}
	// These flags are the launch contract: the server's launcher
	// (satelle-server sty_c9013137) builds `satelle node join --root <dir>
	// --relay <url> --session <id>` and hands over the node token either as
	// `--token <node token>` or, to keep it out of argv, a log and the process
	// list, as `--token-stdin` with the token on the first line of stdin. The
	// session is the node's id on satelle.dev, the key of every route the node
	// calls. The key line on stdout and the node's own log on stderr are the
	// other half of it.
	join.Flags().StringVar(&root, "root", remotenode.NodeRoot, "the node's working root")
	join.Flags().StringVar(&relay, "relay", "", "relay URL")
	join.Flags().StringVar(&session, "session", "", "channel session id, which is the node's id on satelle.dev")
	join.Flags().StringVar(&token, "token", "", "the node's bearer token for satelle.dev and the relay")
	join.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the node's bearer token from the first line of stdin, keeping it out of argv")
	for _, f := range []string{"relay", "session"} {
		_ = join.MarkFlagRequired(f)
	}
	join.MarkFlagsMutuallyExclusive("token", "token-stdin")
	join.MarkFlagsOneRequired("token", "token-stdin")
	node.AddCommand(join)
	register(node)
}

// readTokenStdin reads the node token from r: the first line, or all of r if it
// ends without a newline. It stops at the newline rather than at EOF, since the
// writer (a server's exec stdin stream) may keep stdin open, and it never closes
// r. Its errors are fixed text, so no token can reach them.
func readTokenStdin(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("join: --token-stdin: cannot read stdin")
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("join: --token-stdin: no token on stdin")
	}
	return token, nil
}

// runNodeJoin is the node's side. It runs in the working root: init creates the
// data dir and store, the key is printed for the controller to pin, the node
// fetches and installs its definition, and it then enrolls and serves. However it
// stops, it tells satelle.dev once, which revokes its token.
func runNodeJoin(cmd *cobra.Command, root, relay, session, token string) error {
	client := remotenode.NewNodeClient(nodeServer(), session, token, nodeHTTP)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), nodeEndTimeout)
		defer cancel()
		if err := client.End(ctx); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "join: telling satelle.dev the node has ended: %v\n", err)
		}
	}()

	root, err := expandNodeRoot(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	// noWorkspace: the node is not registered in any workspace. Init's output is
	// the node's own log; stdout is reserved for the key line.
	if err := runInit(cmd.ErrOrStderr(), root, true, nil); err != nil {
		return fmt.Errorf("join: init %s: %w", root, err)
	}

	id, err := nodeIdentity()
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), remotenode.KeyLinePrefix+base64.StdEncoding.EncodeToString(id.Public()))

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataDir := filepath.Join(root, config.DefaultDataDir)
	def, version, err := installNodeDefinition(ctx, cmd, client, dataDir)
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}

	sess, err := remotechan.Join(ctx, relay, session, id, token, nodeChannelOptions...)
	if err != nil {
		var refused *remotechan.RelayStatusError
		if errors.As(err, &refused) && refused.Status == http.StatusUnauthorized {
			return &remotenode.TokenRefusedError{Route: "relay upgrade for session " + session}
		}
		return fmt.Errorf("join: %w", err)
	}
	defer sess.Close()

	node := remotenode.NewNode()
	node.Handle(remotenode.AgentJob, agentJobHandler(dataDir, root, client, def.Secret))
	node.Handle(remotenode.StoryReportJob, remotenode.StoryReportHandler(nodeProcess))
	report, perr := prepareNode(ctx, root, dataDir, def, version)
	if perr != nil {
		report = remotenode.Report{Error: perr.Error()}
	}
	return node.Serve(ctx, sess, report)
}

// installNodeDefinition fetches the node's definition with its node token,
// replaces the node's substrate with the definition's own and reindexes it through
// the same pass `satelle reindex` runs. It returns what the node reads of the
// definition's remote.toml and the version the definition was fetched at. Nothing
// is installed unless the whole bundle checks out.
func installNodeDefinition(ctx context.Context, cmd *cobra.Command, client *remotenode.NodeClient, dataDir string) (remotedef.Definition, string, error) {
	bundle, version, err := client.Definition(ctx)
	if err != nil {
		return remotedef.Definition{}, "", err
	}
	// The store opens against what init scaffolded; the remote's own bindings are
	// judged later by resolving them, not by the bootstrap.
	if err := openAppForCmd(cmd); err != nil {
		return remotedef.Definition{}, "", err
	}
	a, err := appFrom(cmd)
	if err != nil {
		return remotedef.Definition{}, "", err
	}
	def, err := remotenode.InstallDefinition(dataDir, bundle)
	if err != nil {
		return remotedef.Definition{}, "", fmt.Errorf("definition: %w", err)
	}
	if err := reindexSubstrate(cmd, a, false); err != nil {
		return remotedef.Definition{}, "", fmt.Errorf("reindex: %w", err)
	}
	return def, version, nil
}

func expandNodeRoot(root string) (string, error) {
	if root == remotenode.NodeRoot || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(root, "~/")), nil
	}
	return filepath.Abs(root)
}

// prepareNode readies the node from the definition it installed, in order: fetch
// the repos and resolve every binding. Anything that fails is the node's error,
// which the controller reports.
func prepareNode(ctx context.Context, root, dataDir string, def remotedef.Definition, version string) (remotenode.Report, error) {
	repos, err := remotenode.FetchRepos(ctx, filepath.Join(root, "repos"), def.Repos, nodeFetchEnv)
	if err != nil {
		return remotenode.Report{}, err
	}
	bindings, err := resolveNodeBindings(dataDir)
	if err != nil {
		return remotenode.Report{}, err
	}
	categories, err := doneCategories(dataDir)
	if err != nil {
		return remotenode.Report{}, err
	}
	process, procRoot, err := nodeProcess()
	if err != nil {
		return remotenode.Report{}, err
	}
	return remotenode.Report{
		Version: buildinfo.Resolve().Version, DefinitionVersion: version, Workflows: categories,
		Allow: def.Allow(), Repos: repos, Bindings: bindings,
		Now: time.Now().UnixNano(), Hashes: config.ProcessHashes(process, procRoot),
	}, nil
}

// nodeProcess is the node's process config and the root it is anchored at: the
// node's working root, which join has made the working directory.
func nodeProcess() (config.Config, string, error) {
	process, _, processRoot, _, err := config.LoadInvokingProcess()
	if err != nil {
		return config.Config{}, "", fmt.Errorf("the node's process config: %w", err)
	}
	return process, processRoot, nil
}

// resolveNodeBindings resolves every binding the remote's agents.toml defines
// through agentstep.LookupRunner, the resolution a dispatch uses, and names every
// one that does not resolve.
func resolveNodeBindings(dataDir string) ([]string, error) {
	cfg, err := config.LoadAgents(dataDir)
	if err != nil {
		return nil, fmt.Errorf("agents.toml: %w", err)
	}
	all := map[string]config.AgentBinding{}
	if cfg.Executor.Command != "" {
		all["executor"] = cfg.Executor
	}
	if cfg.Reviewer.Command != "" {
		all["reviewer"] = cfg.Reviewer
	}
	for n, b := range cfg.Agents {
		all[n] = b
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)

	var resolved []string
	var failed []error
	for _, n := range names {
		b := all[n]
		iface := b.ResolvedInterface()
		if iface == config.InterfaceCloud {
			continue // dispatched by the platform, not run by this node
		}
		if _, err := agentstep.LookupRunner(iface, b.CommandTemplate()); err != nil {
			failed = append(failed, fmt.Errorf("binding %q: %w", n, err))
			continue
		}
		resolved = append(resolved, n)
	}
	if len(failed) > 0 {
		return nil, errors.Join(failed...)
	}
	return resolved, nil
}

// doneCategories lists the workflow categories of the remote's own done.toml.
func doneCategories(dataDir string) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(dataDir, config.AgentsConfigDir, "done.toml"))
	if err != nil {
		return nil, fmt.Errorf("the remote's done.toml: %w", err)
	}
	lists, err := wfdot.ParseDone(string(body))
	if err != nil {
		return nil, err
	}
	out := make([]string, len(lists))
	for i, l := range lists {
		out[i] = l.Category
	}
	return out, nil
}

// agentJobHandler answers an agent job by running the remote's agent binding on
// the message: the executor if it names a command, else the first agent binding
// by name that does. The binding's env is not applied here. The agent gets the
// secrets the definition declares, read from satelle.dev with the node token, in
// its process environment alone. A secret satelle.dev refuses stops the job
// before the agent starts; a refused node token also stops the node (Serve).
func agentJobHandler(dataDir, root string, client *remotenode.NodeClient, decls []remotedef.SecretDecl) remotenode.Handler {
	return func(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
		var req remotenode.AgentRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		cfg, err := config.LoadAgents(dataDir)
		if err != nil {
			return nil, err
		}
		b, name, ok := talkBinding(cfg)
		if !ok {
			return nil, errors.New("the remote defines no agent binding that runs a command")
		}
		runner, err := agentstep.LookupRunner(b.ResolvedInterface(), b.CommandTemplate())
		if err != nil {
			return nil, fmt.Errorf("binding %q: %w", name, err)
		}
		secrets, err := client.Secrets(ctx, decls)
		if err != nil {
			return nil, err
		}
		raw, err := runner.Run(ctx, agentcli.Request{
			SystemPrompt: "You are the agent on a remote node. Answer the developer's message.",
			Payload:      req.Text, AllowedTools: b.Tools, Model: b.Model, Dir: root,
			Env: secrets,
		})
		if err != nil {
			return nil, fmt.Errorf("binding %q: %w", name, err)
		}
		text, _ := agentcli.UnwrapUsage(raw)
		return json.Marshal(remotenode.AgentReply{Text: strings.TrimSpace(string(text))})
	}
}

// talkBinding picks the binding that answers a message.
func talkBinding(cfg config.AgentsConfig) (config.AgentBinding, string, bool) {
	runs := func(b config.AgentBinding) bool {
		return b.CommandTemplate() != "" && !config.IsInLoopCommand(b.CommandTemplate()) &&
			b.ResolvedInterface() != config.InterfaceCloud
	}
	if runs(cfg.Executor) {
		return cfg.Executor, "executor", true
	}
	names := make([]string, 0, len(cfg.Agents))
	for n := range cfg.Agents {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if runs(cfg.Agents[n]) {
			return cfg.Agents[n], n, true
		}
	}
	return config.AgentBinding{}, "", false
}
