// `satelle remote start <name>` starts a fresh node from a local remote
// definition and `satelle remote join` is what runs on that node. start uses only
// the definition's own host commands; join creates the node's data dir and store,
// unpacks the substrate local satelle sends over the sealed channel, reindexes it,
// fetches the declared repos read-only, and reports ready.
package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/release"
	"github.com/bobmcallan/satelle/internal/remotechan"
	"github.com/bobmcallan/satelle/internal/remotedef"
	"github.com/bobmcallan/satelle/internal/remotenode"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// Seams a test replaces; production leaves them alone.
var (
	// remoteHostedClient is the signed-in client the relay dial uses.
	remoteHostedClient = func(server string) *hosted.Client {
		return hosted.NewClient(server, hosted.DefaultStore(), nil)
	}
	// remoteChannelOptions configure local satelle's end of the channel.
	remoteChannelOptions []remotechan.Option
	// remoteNodeChannelOptions configure the node's end of the channel.
	remoteNodeChannelOptions []remotechan.Option
	// remoteNodeIdentity makes the node's key.
	remoteNodeIdentity = remotechan.NewIdentity
)

// remoteStartCommands are the `remote start` and `remote join` commands, attached
// to the remote group by cmd_remote.go.
func remoteStartCommands() []*cobra.Command {
	var server, message string
	start := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a fresh node from a remote definition and print its ready report",
		Long: `Validates the remote definition and starts a node from it using only the
definition's own host commands: host.start, host.network (with the egress
allowlist), then a held host.run that installs the pinned satelle, runs
host.bootstrap and joins. The node enrolls on the sealed channel through the
hosted relay, is sent the definition's workflows, skills and principles, fetches
its declared repos read-only, and reports ready; the report is printed.

--message sends the node's agent one message as a job and prints its reply.
When the run ends host.stop runs if the definition's lifecycle is "shutdown"
(the default) and is skipped if it is "idle". Needs a signed-in satelle (satelle
login): the relay is the hosted server's.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemoteStart(cmd, args[0], server, message)
		},
	}
	start.Flags().StringVar(&server, "server", "", "Hosted server URL (overrides the configured machine hosted server)")
	start.Flags().StringVar(&message, "message", "", "send the node's agent this message and print its reply")

	var root, relay, session, token string
	join := &cobra.Command{
		Use:    "join",
		Short:  "Run on a remote node: set up from the substrate local satelle sends and serve its jobs",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemoteJoin(cmd, root, relay, session, token)
		},
	}
	join.Flags().StringVar(&root, "root", remotenode.NodeRoot, "the node's working root")
	join.Flags().StringVar(&relay, "relay", "", "relay URL")
	join.Flags().StringVar(&session, "session", "", "channel session id")
	join.Flags().StringVar(&token, "token", "", "single-use enrollment token")
	for _, f := range []string{"relay", "session", "token"} {
		_ = join.MarkFlagRequired(f)
	}

	return []*cobra.Command{start, join}
}

func runRemoteStart(cmd *cobra.Command, name, serverFlag, message string) (err error) {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	// Host command output arrives on goroutines while the report is printed from
	// this one; one lock covers every write the command makes to its streams.
	out := lockedWriter{cmd.OutOrStdout()}
	hostLog := lockedWriter{cmd.ErrOrStderr()}
	if !remotedef.ValidName(name) {
		return fmt.Errorf("remote name %q must be lower-case letters, digits, - or _, starting with a letter or digit", name)
	}
	dir := remotedef.Dir(remoteDataDir(), name)
	if _, serr := os.Stat(dir); serr != nil {
		return fmt.Errorf("no remote %q under %s/%s", name, config.DefaultDataDir, config.RemotesDir)
	}
	if verr := reportRemote(cmd, name, dir); verr != nil {
		return verr
	}
	def, err := remotedef.Load(dir)
	if err != nil {
		return err
	}

	srv := resolveServer(serverFlag)
	if srv == "" {
		return errors.New("no hosted server configured — run \"satelle login\"")
	}
	u, err := url.Parse(srv)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("hosted server %q is not a URL", srv)
	}
	releaseBase := os.Getenv("SATELLE_RELEASE_BASE")
	allow := remotedef.Allowlist(def, releaseHosts(releaseBase), u.Hostname())

	env, err := hostEnv(a, def)
	if err != nil {
		return fmt.Errorf("remote %s: host.env: %w", name, err)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := remoteHostedClient(srv)
	run, err := remotenode.Start(ctx, remotenode.StartOptions{
		Name: name, Def: def, DefDir: dir, Allow: allow,
		Client: client, Secrets: client, RelayURL: strings.TrimRight(srv, "/") + "/relay",
		Env: env, ReleaseBase: releaseBase,
		Recorder: a.Store.Ledger, Log: hostLog, Channel: remoteChannelOptions,
	})
	if err != nil {
		return fmt.Errorf("remote %s: %w", name, err)
	}
	defer func() {
		endErr := run.End(ctx)
		lifecycle := def.EffectiveLifecycle()
		switch {
		case endErr != nil:
			err = errors.Join(err, fmt.Errorf("remote %s: ending the run: %w", name, endErr))
		case lifecycle == remotedef.LifecycleIdle:
			fmt.Fprintf(out, "remote %s: left idle\n", name)
		default:
			fmt.Fprintf(out, "remote %s: stopped\n", name)
		}
	}()

	printReport(out, name, run.Report)
	if message == "" {
		return nil
	}
	body, err := run.Request(ctx, remotenode.AgentJob, remotenode.AgentRequest{Text: message})
	if err != nil {
		return fmt.Errorf("remote %s: agent: %w", name, err)
	}
	rep, err := remotenode.DecodeAgentReply(body)
	if err != nil {
		return fmt.Errorf("remote %s: agent reply: %w", name, err)
	}
	fmt.Fprintln(out, rep.Text)
	return nil
}

// remoteOutMu serialises every write `remote start` makes to either stream.
var remoteOutMu sync.Mutex

// lockedWriter writes to w under remoteOutMu.
type lockedWriter struct{ w io.Writer }

func (l lockedWriter) Write(p []byte) (int, error) {
	remoteOutMu.Lock()
	defer remoteOutMu.Unlock()
	return l.w.Write(p)
}

func printReport(out io.Writer, name string, r remotenode.Report) {
	fmt.Fprintf(out, "remote %s: ready\n", name)
	fmt.Fprintf(out, "  satelle    %s\n", r.Version)
	fmt.Fprintf(out, "  workflows  %s\n", strings.Join(r.Workflows, ", "))
	fmt.Fprintf(out, "  bindings   %s\n", strings.Join(r.Bindings, ", "))
	fmt.Fprintf(out, "  allow      %s\n", strings.Join(r.Allow, ", "))
	if len(r.Repos) == 0 {
		fmt.Fprintln(out, "  repos      (none)")
	}
	for _, repo := range r.Repos {
		fmt.Fprintf(out, "  repo       %s @ %s (%s)\n", repo.Name, repo.Ref, shortCommit(repo.Commit))
	}
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// hostEnv expands the definition's host.env — ${VAR} references resolve against
// the layered [vars] and, for names those do not define, the process
// environment — into KEY=VALUE pairs.
func hostEnv(a *app.App, def remotedef.Definition) ([]string, error) {
	if len(def.Host.Env) == 0 {
		return nil, nil
	}
	eff, err := config.LoadEffectiveAgents(a.DataDir, a.PlaneConfig().Vars)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	for k, v := range eff.Vars {
		vars[k] = v
	}
	keys := make([]string, 0, len(def.Host.Env))
	for k := range def.Host.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v, err := config.ExpandVars(def.Host.Env[k], vars)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out = append(out, k+"="+v)
	}
	return out, nil
}

// runRemoteJoin is the node's side. It runs in the working root: init creates the
// data dir and store, the key is printed for local satelle to pin, and the node
// then enrolls and serves.
func runRemoteJoin(cmd *cobra.Command, root, relay, session, token string) error {
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

	id, err := remoteNodeIdentity()
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), remotenode.KeyLinePrefix+base64.StdEncoding.EncodeToString(id.Public()))

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sess, err := remotechan.Join(ctx, relay, session, id, token, remoteNodeChannelOptions...)
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}
	defer sess.Close()

	node := remotenode.NewNode()
	dataDir := filepath.Join(root, config.DefaultDataDir)
	// The agent's `satelle secret get` reaches this process over a socket. A node
	// that cannot serve one (a path too long for a socket) still serves jobs, and
	// the binding's ${VAR}s still resolve at launch.
	if closeSock, serr := node.ServeSocket(ctx, remotenode.SocketPath(dataDir)); serr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "join: no secret socket: %v\n", serr)
	} else {
		defer closeSock()
	}
	node.Handle(remotenode.AgentJob, agentJobHandler(dataDir, root, node.FetchSecret))
	return node.Serve(ctx, sess, func(ctx context.Context, s remotenode.Setup) (remotenode.Report, error) {
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
// Anything that fails is the node's error, which local satelle prints.
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
	return remotenode.Report{
		Version: buildinfo.Resolve().Version, Workflows: categories,
		Allow: s.Allow, Repos: repos, Bindings: bindings,
	}, nil
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
// by name that does. Before the agent starts, the ${VAR} references in the
// binding's env are resolved with values fetched for this job through fetch, and
// only the resolved env reaches the agent's process, in memory.
func agentJobHandler(dataDir, root string, fetch remotenode.SecretFetch) remotenode.Handler {
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
		env, settings, err := resolveBindingSecrets(ctx, b, fetch)
		if err != nil {
			return nil, fmt.Errorf("binding %q: %w", name, err)
		}
		raw, err := runner.Run(ctx, agentcli.Request{
			SystemPrompt: "You are the agent on a satelle remote node. Answer the developer's message.",
			Payload:      req.Text, AllowedTools: b.Tools, Model: b.Model, Settings: settings, Env: env, Dir: root,
		})
		if err != nil {
			return nil, fmt.Errorf("binding %q: %w", name, err)
		}
		text, _ := agentcli.UnwrapUsage(raw)
		return json.Marshal(remotenode.AgentReply{Text: strings.TrimSpace(string(text))})
	}
}

// resolveBindingSecrets resolves b's env and settings for one job: each ${VAR}
// they reference is fetched through fetch (the sealed path to local satelle, which
// checks the remote's definition and grant) and expanded by
// config.ResolveAgentEnvs, the one owner of binding ${VAR} expansion. It returns
// exactly the binding's own env and settings, resolved; a definition secret the
// binding does not reference is never fetched. A refused key fails the job
// before the agent starts. Errors name the key, never a value.
func resolveBindingSecrets(ctx context.Context, b config.AgentBinding, fetch remotenode.SecretFetch) (env map[string]string, settings string, err error) {
	refs := config.VarRefs(b)
	vars := make(map[string]string, len(refs))
	for _, key := range refs {
		if fetch == nil {
			return nil, "", fmt.Errorf("secret %s: this node has no secret channel", key)
		}
		v, ferr := fetch(ctx, remotenode.JobID(ctx), key)
		if ferr != nil {
			return nil, "", ferr
		}
		vars[key] = v
	}
	res, err := config.ResolveAgentEnvs(config.AgentsConfig{Executor: b}, vars)
	if err != nil {
		return nil, "", err
	}
	if len(res.Executor.Settings) > 0 {
		sb, merr := json.Marshal(res.Executor.Settings)
		if merr != nil {
			return nil, "", merr
		}
		settings = string(sb)
	}
	return res.Executor.Env, settings, nil
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

// releaseHosts is every host the node's install reaches: the release hosts and,
// when SATELLE_RELEASE_BASE is forwarded to the node (a mirror), the mirror's
// host, which the node's own egress allowlist would otherwise block.
func releaseHosts(base string) []string {
	hosts := release.Hosts()
	if u, err := url.Parse(strings.TrimSpace(base)); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	return hosts
}
