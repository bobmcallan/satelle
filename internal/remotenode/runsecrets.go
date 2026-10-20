package remotenode

// RunSecrets is what a node needs to read the secrets of one run from
// satelle.dev, and nothing more: the server's id for the run, the remote's id
// and the server's base URL, the secrets by key, scope and workspace, and how
// the remote proves itself. It holds NAMES, never a value and never a key. It
// travels two ways that cannot drift because both are this type:
//
//   - the controller seals it into Setup (Setup.Run), and the node's agent
//     handler reads from it;
//   - the launcher exports it as SATELLE_SERVER_RUN_ID and the SATELLE_REMOTE_*
//     variables (Env) to the process on the node, where `satelle story drive`
//     reads it back (RunSecretsFromEnv).

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/remotedef"
)

// runIDPattern is the shape of a run id: it goes into a signed path unescaped.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SecretRef is one secret the run reads: its key, the scope it is stored under
// and, for team scope, the workspace.
type SecretRef struct {
	Key       string `json:"key"`
	Scope     string `json:"scope"`
	Workspace string `json:"workspace,omitempty"`
}

// RunSecrets is the carrier described above.
type RunSecrets struct {
	// ServerRunID is satelle.dev's id for the run: the fire's trigger_id for a
	// managed remote, the owner's registered run for a local-started one.
	ServerRunID string `json:"server_run_id,omitempty"`
	// RemoteID is the remote's id on satelle.dev (rmt_…).
	RemoteID string `json:"remote_id"`
	// Server is the satelle.dev base URL (config.ResolveHostedServer).
	Server string `json:"server"`
	// Identity is how the remote proves itself: "key:<ENV>" names the host
	// environment variable holding the private key, "oidc:<argv>" a platform
	// workload identity token command.
	Identity string      `json:"identity"`
	Secrets  []SecretRef `json:"secrets"`
}

// Active reports whether there is anything to read: secrets declared and a run
// to read them for.
func (r RunSecrets) Active() bool { return len(r.Secrets) > 0 && r.ServerRunID != "" }

// Validate says why the carrier cannot be read from, naming the field and never a
// value.
func (r RunSecrets) Validate() error {
	var errs []error
	if !runIDPattern.MatchString(r.ServerRunID) {
		errs = append(errs, errors.New("no server run id"))
	}
	if !remotedef.ValidRemoteID(r.RemoteID) {
		errs = append(errs, errors.New("no remote id"))
	}
	if r.Server == "" {
		errs = append(errs, errors.New("no satelle.dev server"))
	}
	if r.IdentityEnv() == "" && !r.IsOIDC() {
		errs = append(errs, errors.New("no identity: the remote declares neither identity.key nor identity.oidc"))
	}
	for _, s := range r.Secrets {
		if s.Key == "" || (s.Scope != remotedef.ScopePersonal && s.Scope != remotedef.ScopeTeam) {
			errs = append(errs, fmt.Errorf("secret %q has no valid scope", s.Key))
		}
	}
	return errors.Join(errs...)
}

// IdentityEnv is the host environment variable that holds the remote's private
// key, or "" when the remote proves itself another way.
func (r RunSecrets) IdentityEnv() string {
	v, _ := strings.CutPrefix(r.Identity, "key:")
	if v == r.Identity {
		return ""
	}
	return v
}

// IsOIDC reports whether the remote proves itself with a workload identity.
func (r RunSecrets) IsOIDC() bool { return strings.HasPrefix(r.Identity, "oidc:") }

// OIDCTokenArgv is the command that prints an identity token, with {aud} still in
// it.
func (r RunSecrets) OIDCTokenArgv() ([]string, error) {
	raw, ok := strings.CutPrefix(r.Identity, "oidc:")
	if !ok {
		return nil, errors.New("the identity is not a workload identity")
	}
	var argv []string
	if err := json.Unmarshal([]byte(raw), &argv); err != nil || len(argv) == 0 {
		return nil, errors.New("the workload identity carries no token command")
	}
	return argv, nil
}

// Keys are the declared secret keys, in order.
func (r RunSecrets) Keys() []string {
	out := make([]string, len(r.Secrets))
	for i, s := range r.Secrets {
		out[i] = s.Key
	}
	return out
}

// Env is the carrier as environment entries. The run id is left out when empty
// (a remote whose run is not yet known carries the rest).
func (r RunSecrets) Env() []string {
	var out []string
	if r.ServerRunID != "" {
		out = append(out, config.RemoteServerRunEnv+"="+r.ServerRunID)
	}
	out = append(out,
		config.RemoteIDEnv+"="+r.RemoteID,
		config.RemoteServerEnv+"="+r.Server,
		config.RemoteSecretsEnv+"="+encodeSecretRefs(r.Secrets),
		config.RemoteIdentityEnv+"="+r.Identity)
	return out
}

// RunSecretsFromEnv reads the carrier back from getenv, the inverse of Env.
func RunSecretsFromEnv(getenv func(string) string) RunSecrets {
	return RunSecrets{
		ServerRunID: getenv(config.RemoteServerRunEnv),
		RemoteID:    getenv(config.RemoteIDEnv),
		Server:      getenv(config.RemoteServerEnv),
		Identity:    getenv(config.RemoteIdentityEnv),
		Secrets:     decodeSecretRefs(getenv(config.RemoteSecretsEnv)),
	}
}

// SecretRefs are a definition's declared secrets as the carrier names them.
func SecretRefs(def remotedef.Definition) []SecretRef {
	out := make([]SecretRef, len(def.Secret))
	for i, s := range def.Secret {
		out[i] = SecretRef{Key: s.Key, Scope: s.Scope, Workspace: s.Workspace}
	}
	return out
}

// NewRunSecrets is the carrier for def's declared secrets. remoteID and server are
// the remote's id and the satelle.dev base; runID may be empty until the run is
// known.
func NewRunSecrets(def remotedef.Definition, remoteID, server, runID string) RunSecrets {
	return RunSecrets{ServerRunID: runID, RemoteID: remoteID, Server: server, Identity: def.IdentityRef(), Secrets: SecretRefs(def)}
}

func encodeSecretRefs(refs []SecretRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.Key + ":" + r.Scope
		if r.Workspace != "" {
			parts[i] += ":" + r.Workspace
		}
	}
	return strings.Join(parts, ",")
}

func decodeSecretRefs(s string) []SecretRef {
	if s == "" {
		return nil
	}
	var out []SecretRef
	for _, part := range strings.Split(s, ",") {
		f := strings.SplitN(part, ":", 3)
		ref := SecretRef{Key: f[0]}
		if len(f) > 1 {
			ref.Scope = f[1]
		}
		if len(f) > 2 {
			ref.Workspace = f[2]
		}
		out = append(out, ref)
	}
	return out
}
