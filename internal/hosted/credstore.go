// Package hosted is the client half of satelle's hosted-server integration
// (sty_2fc93374): the OAuth 2.1 + PKCE login flow, a per-user credential store,
// and an authenticated HTTP client with transparent refresh-on-401.
//
// All of this is MECHANISM (a CLI verb + an HTTP client), so it lives in the
// binary — no process/gate logic. Credentials are stored per USER, OUTSIDE any
// repo working tree (at $XDG_CONFIG_HOME/satelle/credentials.toml), mirroring
// the satellites cliconfig model: OAuth tokens are per-user secrets that must
// never sit in a repo, and one login then serves every repo pointing at the same
// server.
package hosted

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrNoCredential signals no stored credential for the requested server.
var ErrNoCredential = errors.New("hosted: no stored credential (run \"satelle login\")")

// Credential is one server's persisted OAuth token set. The access token is a
// short-lived JWT (~1h); the refresh token is opaque and ROTATES on every
// refresh grant, so Save must persist the rotated value immediately.
type Credential struct {
	ServerURL    string `toml:"server_url"`
	AccessToken  string `toml:"access_token"`
	RefreshToken string `toml:"refresh_token"`
	TokenType    string `toml:"token_type,omitempty"`
	Scope        string `toml:"scope,omitempty"`
	// ExpiresAt is the access token's expiry as RFC3339 (advisory — refresh is
	// driven by an actual 401, not by this clock).
	ExpiresAt string `toml:"expires_at,omitempty"`
	CreatedAt string `toml:"created_at,omitempty"`
}

// credentialsFile is the on-disk TOML shape: an array of [[credential]] tables,
// one per server, so multiple hosted servers coexist for the same user.
type credentialsFile struct {
	Credential []Credential `toml:"credential"`
}

// FileStore is the default user-level credential store. It satisfies Store.
type FileStore struct {
	// Path overrides the default location (used by tests). Empty → CredentialsPath.
	Path string
}

// Store is the credential persistence seam the Client depends on — the file
// store in production, an in-memory fake in tests.
type Store interface {
	Load(serverURL string) (Credential, error)
	Save(cred Credential) error
	Delete(serverURL string) error
}

// CredentialsPath returns the per-user credentials file location:
// $XDG_CONFIG_HOME/satelle/credentials.toml, falling back to
// ~/.config/satelle/credentials.toml. Deliberately outside any repo tree.
func CredentialsPath() (string, error) {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "satelle", "credentials.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("hosted: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "satelle", "credentials.toml"), nil
}

func (s FileStore) path() (string, error) {
	if strings.TrimSpace(s.Path) != "" {
		return s.Path, nil
	}
	return CredentialsPath()
}

// normalizeServerURL trims trailing slashes/space so lookups are stable.
func normalizeServerURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

func (s FileStore) read() (credentialsFile, string, error) {
	path, err := s.path()
	if err != nil {
		return credentialsFile{}, "", err
	}
	var cf credentialsFile
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return credentialsFile{}, path, nil
		}
		return credentialsFile{}, path, fmt.Errorf("hosted: read %s: %w", path, err)
	}
	if err := toml.Unmarshal(b, &cf); err != nil {
		return credentialsFile{}, path, fmt.Errorf("hosted: parse %s: %w", path, err)
	}
	return cf, path, nil
}

// Load returns the credential for serverURL, or ErrNoCredential.
func (s FileStore) Load(serverURL string) (Credential, error) {
	cf, _, err := s.read()
	if err != nil {
		return Credential{}, err
	}
	want := normalizeServerURL(serverURL)
	for _, c := range cf.Credential {
		if normalizeServerURL(c.ServerURL) == want {
			return c, nil
		}
	}
	return Credential{}, ErrNoCredential
}

// Save upserts cred by server_url and writes the file atomically (tmp + rename,
// file 0600, dir 0700). ServerURL and both tokens must be non-empty.
func (s FileStore) Save(cred Credential) error {
	cred.ServerURL = normalizeServerURL(cred.ServerURL)
	if cred.ServerURL == "" {
		return fmt.Errorf("hosted: credential has no server_url")
	}
	if cred.AccessToken == "" || cred.RefreshToken == "" {
		return fmt.Errorf("hosted: credential missing access/refresh token")
	}
	cf, path, err := s.read()
	if err != nil {
		return err
	}
	replaced := false
	for i := range cf.Credential {
		if normalizeServerURL(cf.Credential[i].ServerURL) == cred.ServerURL {
			cf.Credential[i] = cred
			replaced = true
			break
		}
	}
	if !replaced {
		cf.Credential = append(cf.Credential, cred)
	}
	return writeCredentials(path, cf)
}

// Delete removes serverURL's credential (a no-op if absent). When the last
// credential is removed the file is left empty rather than deleted.
func (s FileStore) Delete(serverURL string) error {
	cf, path, err := s.read()
	if err != nil {
		return err
	}
	want := normalizeServerURL(serverURL)
	kept := cf.Credential[:0]
	for _, c := range cf.Credential {
		if normalizeServerURL(c.ServerURL) != want {
			kept = append(kept, c)
		}
	}
	cf.Credential = kept
	return writeCredentials(path, cf)
}

func writeCredentials(path string, cf credentialsFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("hosted: create %s: %w", filepath.Dir(path), err)
	}
	var sb strings.Builder
	if err := toml.NewEncoder(&sb).Encode(cf); err != nil {
		return fmt.Errorf("hosted: encode credentials: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("hosted: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosted: rename %s: %w", path, err)
	}
	return nil
}
