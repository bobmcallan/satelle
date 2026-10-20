package remotedef

// The secrets a remote reads from satelle.dev (sty_0b1c72a9) are declared here,
// each as a [[secret]]: the key, the scope it is stored under, where local
// satelle gets its value, and how long that value lives. The definition never
// holds a value. The remote's server id is a declared field too, so every call
// that addresses the remote on satelle.dev takes it from configuration.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/health"
)

// The two scopes a secret is stored under on satelle.dev.
const (
	ScopePersonal = "personal"
	ScopeTeam     = "team"
)

// FromEnv is the value of `from` that reads the key from the local .env file.
const FromEnv = "env"

// SecretDecl is one [[secret]]. Key is the name the secret is stored and read
// under; Scope is ScopePersonal or ScopeTeam; From is FromEnv or an argv that
// prints the value; Expires (a date) or TTL (a duration) says when the value
// lapses, at most one of them. Workspace is the team workspace id, required for
// team scope only.
type SecretDecl struct {
	Key       string `toml:"key"`
	Scope     string `toml:"scope"`
	From      any    `toml:"from"`
	Expires   string `toml:"expires"`
	TTL       string `toml:"ttl"`
	Workspace string `toml:"workspace"`
}

// IsEnv reports whether the value is read from the local .env.
func (s SecretDecl) IsEnv() bool {
	f, ok := s.From.(string)
	return ok && f == FromEnv
}

// Argv is the command that mints the value, or nil when From is not an argv.
func (s SecretDecl) Argv() []string {
	list, ok := s.From.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		str, ok := a.(string)
		if !ok {
			return nil
		}
		out = append(out, str)
	}
	return out
}

// ExpiresAt is the declared expiry date, when there is one.
func (s SecretDecl) ExpiresAt() (time.Time, bool) {
	if s.Expires == "" {
		return time.Time{}, false
	}
	t, err := parseExpires(s.Expires)
	return t, err == nil
}

// TTLDuration is the declared lifetime, when there is one.
func (s SecretDecl) TTLDuration() (time.Duration, bool) {
	if s.TTL == "" {
		return 0, false
	}
	d, err := ParseTTL(s.TTL)
	return d, err == nil
}

// ParseTTL reads a ttl: a Go duration ("36h"), or whole days ("30d").
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("%q is not a positive number of days", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a positive duration such as 36h or 30d", s)
	}
	return d, nil
}

func parseExpires(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not an RFC 3339 time or a YYYY-MM-DD date", s)
	}
	return t.UTC(), nil
}

// OIDCIdentity is a platform workload identity: the remote proves itself with a
// short-lived token the platform issues instead of a key. Token is the argv that
// prints one for an audience, with {aud} standing for it; satelle runs it on the
// host through host.run. Issuer and Subject are what satelle.dev binds.
type OIDCIdentity struct {
	Issuer  string   `toml:"issuer"`
	Subject string   `toml:"subject"`
	Token   []string `toml:"token"`
}

// PlaceholderAud is the argv token an identity's token command carries where the
// audience goes.
const PlaceholderAud = "{aud}"

// HasSecrets reports whether the definition declares any [[secret]].
func (d Definition) HasSecrets() bool { return len(d.Secret) > 0 }

// IdentityRef names how the remote proves itself, in the form a node reads it
// back from its environment: "key:<ENV>" for a key held in the host's secret
// store, "oidc:<token argv as a JSON array>" for a platform workload identity,
// "" for neither. It holds a variable name and a command, never a key.
func (d Definition) IdentityRef() string {
	switch {
	case d.Identity.OIDC != nil:
		argv, _ := json.Marshal(d.Identity.OIDC.Token)
		return "oidc:" + string(argv)
	case d.Identity.Key != "":
		return "key:" + d.Identity.Key
	}
	return ""
}

// TokenArgv is the identity's token command with {aud} filled, through the one
// placeholder resolver.
func (d Definition) TokenArgv(aud string) []string {
	if d.Identity.OIDC == nil {
		return nil
	}
	return d.HostArgv(d.Identity.OIDC.Token, "", nil, aud)
}

var remoteIDPattern = regexp.MustCompile(`^rmt_[A-Za-z0-9_-]+$`)

// ValidRemoteID reports whether id has the shape of a satelle.dev remote id.
func ValidRemoteID(id string) bool { return remoteIDPattern.MatchString(id) }

// RequireRemoteID is the remote's satelle.dev id, or the one refusal every
// id-scoped call gives when the definition has none. name is the remote's name.
func (d Definition) RequireRemoteID(name string) (string, error) {
	if d.RemoteID == "" {
		return "", fmt.Errorf("remote %s: remote_id is not set; register the remote on satelle.dev, then set remote_id = \"rmt_…\" in its definition", name)
	}
	return d.RemoteID, nil
}

// StoryDrives reports whether host.story_command works the story with
// `satelle story drive`, the command that reads the run's secrets.
func (d Definition) StoryDrives() bool {
	f := strings.Fields(d.Host.StoryCommand)
	return len(f) >= 3 && filepath.Base(f[0]) == "satelle" && f[1] == "story" && f[2] == "drive"
}

// secretFields are the keys a [[secret]] table may carry.
var secretFields = map[string]bool{"key": true, "scope": true, "from": true, "expires": true, "ttl": true, "workspace": true}

var workspaceIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// checkSecrets judges every [[secret]], remote_id and the identity key/oidc.
// Each finding names the secret as secret[<i>] (<KEY>), and none carries a value.
func (v *validation) checkSecrets(def Definition, raw map[string]any) {
	if def.RemoteID != "" && !ValidRemoteID(def.RemoteID) {
		v.fail(DefinitionFile, "remote_id", "must be the id satelle.dev gave the remote, rmt_ and letters, digits, - or _")
	}
	rawSecrets, _ := raw["secret"].([]map[string]any)
	seen := map[string]int{}
	for i, s := range def.Secret {
		at := fmt.Sprintf("secret[%d] (%s)", i, s.Key)
		if i < len(rawSecrets) {
			for _, k := range sortedKeys(rawSecrets[i]) {
				if !secretFields[k] {
					v.fail(DefinitionFile, at+"."+k, "is not a secret field; a value is never declared, name where it comes from with from")
				}
			}
		}
		switch {
		case !envKeyPattern.MatchString(s.Key):
			v.fail(DefinitionFile, at+".key", "is not a well-formed key name (A-Z, 0-9 and _)")
		default:
			if j, dup := seen[s.Key]; dup {
				v.fail(DefinitionFile, at+".key", fmt.Sprintf("duplicate of secret[%d]", j))
			} else {
				seen[s.Key] = i
			}
		}
		switch s.Scope {
		case ScopePersonal:
			if s.Workspace != "" {
				v.fail(DefinitionFile, at+".workspace", "belongs to team scope only")
			}
		case ScopeTeam:
			if !workspaceIDPattern.MatchString(s.Workspace) {
				v.fail(DefinitionFile, at+".workspace", "required for team scope: the team workspace's id")
			}
		default:
			v.fail(DefinitionFile, at+".scope", fmt.Sprintf("%q is not %s or %s", s.Scope, ScopePersonal, ScopeTeam))
		}
		v.checkFrom(at, s)
		if s.Expires != "" && s.TTL != "" {
			v.fail(DefinitionFile, at, "declares both expires and ttl; a secret lapses by one of them")
		}
		if s.Expires != "" {
			if _, err := parseExpires(s.Expires); err != nil {
				v.fail(DefinitionFile, at+".expires", err.Error())
			}
		}
		if s.TTL != "" {
			if _, err := ParseTTL(s.TTL); err != nil {
				v.fail(DefinitionFile, at+".ttl", err.Error())
			}
		}
	}
	v.checkIdentity(def.Identity)
	if def.HasSecrets() && def.Host.StoryCommand != "" && !def.StoryDrives() {
		v.warn(DefinitionFile, "host.story_command", "declares [[secret]] but does not run `satelle story drive`, the command that reads a run's secrets; an agent started any other way gets none")
	}
}

func (v *validation) checkFrom(at string, s SecretDecl) {
	if s.IsEnv() {
		return
	}
	argv := s.Argv()
	if len(argv) == 0 {
		v.fail(DefinitionFile, at+".from", `must be "env" (read the local .env) or an argv list that prints the value; a value is never declared`)
		return
	}
	for i, a := range argv {
		if strings.TrimSpace(a) == "" {
			v.fail(DefinitionFile, fmt.Sprintf("%s.from[%d]", at, i), "is empty")
		}
	}
}

func (v *validation) checkIdentity(id Identity) {
	if id.Key != "" && !envKeyPattern.MatchString(id.Key) {
		v.fail(DefinitionFile, "identity.key", "is not a well-formed environment variable name; it names where the host's secret store puts the private key")
	}
	o := id.OIDC
	if o == nil {
		return
	}
	if id.Key != "" {
		v.fail(DefinitionFile, "identity", "declares both key and oidc; a remote proves itself one way")
	}
	if u, err := url.Parse(o.Issuer); err != nil || u.Scheme != "https" || u.Host == "" {
		v.fail(DefinitionFile, "identity.oidc.issuer", "must be an https URL")
	}
	if strings.TrimSpace(o.Subject) == "" {
		v.fail(DefinitionFile, "identity.oidc.subject", "required")
	}
	v.checkArgv("identity.oidc.token", o.Token)
	if len(o.Token) > 0 && !argvHas(o.Token, PlaceholderAud) {
		v.fail(DefinitionFile, "identity.oidc.token", "must carry "+PlaceholderAud+": the token is minted for one run's audience")
	}
}

// warn records an advisory finding: printed, never blocking.
func (v *validation) warn(file, key, msg string) {
	v.out = append(v.out, health.Warn(health.IDRemoteDefinition, "Remote definition warning", file+": "+key+": "+msg).About(file))
}
