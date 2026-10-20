// `satelle node join` is what runs on a machine acting as a remote node: it
// creates the node's data dir and store, unpacks the substrate the controller
// sends over the sealed channel, reindexes it, fetches the declared repos
// read-only, reports ready and serves the controller's jobs. It is a node mode,
// not a developer command: remotes are defined, published, secreted and
// launched through satelle-server, whose launcher builds this argv.
package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Seams a test replaces; production leaves them alone.
var (
	// nodeChannelOptions configure the node's end of the channel.
	nodeChannelOptions []remotechan.Option
	// nodeIdentity makes the node's key.
	nodeIdentity = remotechan.NewIdentity
)

func init() {
	node := &cobra.Command{
		Use:    "node",
		Short:  "Run this machine as a remote node",
		Hidden: true,
	}
	var root, relay, session, token string
	join := &cobra.Command{
		Use:    "join",
		Short:  "Run on a remote node: set up from the substrate the controller sends and serve its jobs",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodeJoin(cmd, root, relay, session, token)
		},
	}
	// These four flags are the launch contract: the server's launcher
	// (satelle-server sty_ce4239e4) builds `satelle node join --root <dir>
	// --relay <url> --session <id> --token <single-use>`. The key line on stdout
	// and the node's own log on stderr are the other half of it.
	join.Flags().StringVar(&root, "root", remotenode.NodeRoot, "the node's working root")
	join.Flags().StringVar(&relay, "relay", "", "relay URL")
	join.Flags().StringVar(&session, "session", "", "channel session id")
	join.Flags().StringVar(&token, "token", "", "single-use enrollment token")
	for _, f := range []string{"relay", "session", "token"} {
		_ = join.MarkFlagRequired(f)
	}
	node.AddCommand(join)
	register(node)
}

// runNodeJoin is the node's side. It runs in the working root: init creates the
// data dir and store, the key is printed for the controller to pin, and the node
// then enrolls and serves.
func runNodeJoin(cmd *cobra.Command, root, relay, session, token string) error {
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
	sess, err := remotechan.Join(ctx, relay, session, id, token, nodeChannelOptions...)
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}
	defer sess.Close()

	node := remotenode.NewNode()
	dataDir := filepath.Join(root, config.DefaultDataDir)
	// The run the sealed Setup names is what the agent job reads its secrets under.
	var setupRun atomic.Pointer[remotenode.RunSecrets]
	run := func() remotenode.RunSecrets {
		if r := setupRun.Load(); r != nil {
			return *r
		}
		return remotenode.RunSecrets{}
	}
	node.Handle(remotenode.AgentJob, agentJobHandler(dataDir, root, run))
	node.Handle(remotenode.StoryReportJob, remotenode.StoryReportHandler(nodeProcess))
	return node.Serve(ctx, sess, func(ctx context.Context, s remotenode.Setup) (remotenode.Report, error) {
		setupRun.Store(&s.Run)
		return prepareNode(ctx, cmd, root, dataDir, s)
	})
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

// prepareNode readies the node from its setup, in order: open the store init
// created, replace the substrate with the remote's own, reindex it through the
// same pass `satelle reindex` runs, fetch the repos, and resolve every binding.
// Anything that fails is the node's error, which the controller reports.
func prepareNode(ctx context.Context, cmd *cobra.Command, root, dataDir string, s remotenode.Setup) (remotenode.Report, error) {
	// The store opens against what init scaffolded; the remote's own bindings are
	// judged below by resolving them, not by the bootstrap.
	if err := openAppForCmd(cmd); err != nil {
		return remotenode.Report{}, err
	}
	a, err := appFrom(cmd)
	if err != nil {
		return remotenode.Report{}, err
	}
	if err := remotenode.UnpackSubstrate(dataDir, s.Substrate); err != nil {
		return remotenode.Report{}, err
	}
	if err := reindexSubstrate(cmd, a, false); err != nil {
		return remotenode.Report{}, fmt.Errorf("reindex: %w", err)
	}
	repos, err := remotenode.FetchRepos(ctx, filepath.Join(root, "repos"), s.Repos, nil)
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
		Version: buildinfo.Resolve().Version, Workflows: categories,
		Allow: s.Allow, Repos: repos, Bindings: bindings,
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
// secrets the run declares, read from satelle.dev under the run the sealed Setup
// named, in its process environment alone; the node's own private key is dropped
// from it. A secret satelle.dev refuses stops the job before the agent starts.
func agentJobHandler(dataDir, root string, run func() remotenode.RunSecrets) remotenode.Handler {
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
		rs := run()
		var secrets map[string]string
		if rs.Active() {
			values, _, serr := remotenode.SecretsForRun(ctx, rs, os.Getenv)
			if serr != nil {
				return nil, serr
			}
			secrets = values
		}
		raw, err := runner.Run(ctx, agentcli.Request{
			SystemPrompt: "You are the agent on a remote node. Answer the developer's message.",
			Payload:      req.Text, AllowedTools: b.Tools, Model: b.Model, Dir: root,
			Env: secrets, Unset: unsetOf(rs),
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
