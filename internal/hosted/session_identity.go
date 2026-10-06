package hosted

// The principal behind a session token (sty_6ed6318d). A session token cannot
// call GET /api/v1/me (the server refuses it there), so the client learns the
// user id from the session-allowed location registration, which echoes it. Only
// that id is cached — keyed by server plus a one-way fingerprint of the token,
// in a file apart from credentials.toml — so a later process reads who the
// session acts as without a network call. The token value never reaches disk.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

var sessionIdentityMu sync.Mutex

// sessionIdentityEntry is one cached principal.
type sessionIdentityEntry struct {
	Server      string `toml:"server"`
	TokenFP     string `toml:"token_fp"`
	PrincipalID string `toml:"principal_id"`
	ResolvedAt  string `toml:"resolved_at,omitempty"`
}

type sessionIdentityFile struct {
	Session []sessionIdentityEntry `toml:"session"`
}

// SessionIdentityPath is the per-user session-identity file, beside
// credentials.toml and under the same test isolation guard.
func SessionIdentityPath() (string, error) {
	cred, err := CredentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cred), "session-identity.toml"), nil
}

func readSessionIdentity() (sessionIdentityFile, string, error) {
	path, err := SessionIdentityPath()
	if err != nil {
		return sessionIdentityFile{}, "", err
	}
	var f sessionIdentityFile
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sessionIdentityFile{}, path, nil
		}
		return sessionIdentityFile{}, path, fmt.Errorf("hosted: read %s: %w", path, err)
	}
	if err := toml.Unmarshal(b, &f); err != nil {
		return sessionIdentityFile{}, path, fmt.Errorf("hosted: parse %s: %w", path, err)
	}
	return f, path, nil
}

// SessionPrincipal returns the user id cached for this session credential on its
// server, and whether one is known. A token with a different fingerprint — a new
// token, perhaps for another user — is never resolved to an earlier principal.
func SessionPrincipal(cred Credential) (string, bool) {
	fp := SessionFingerprint(cred)
	if fp == "" {
		return "", false
	}
	sessionIdentityMu.Lock()
	defer sessionIdentityMu.Unlock()
	f, _, err := readSessionIdentity()
	if err != nil {
		return "", false
	}
	server := normalizeServerURL(cred.ServerURL)
	for _, e := range f.Session {
		if normalizeServerURL(e.Server) == server && e.TokenFP == fp && e.PrincipalID != "" {
			return e.PrincipalID, true
		}
	}
	return "", false
}

// saveSessionPrincipal records the principal for cred, replacing any earlier
// entry for the same server so the file holds one session per server.
func saveSessionPrincipal(cred Credential, principalID string) error {
	fp := SessionFingerprint(cred)
	if fp == "" || strings.TrimSpace(principalID) == "" {
		return nil
	}
	sessionIdentityMu.Lock()
	defer sessionIdentityMu.Unlock()
	f, path, err := readSessionIdentity()
	if err != nil {
		return err
	}
	server := normalizeServerURL(cred.ServerURL)
	kept := f.Session[:0]
	for _, e := range f.Session {
		if normalizeServerURL(e.Server) != server {
			kept = append(kept, e)
		}
	}
	f.Session = append(kept, sessionIdentityEntry{
		Server: server, TokenFP: fp, PrincipalID: principalID,
		ResolvedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("hosted: create %s: %w", filepath.Dir(path), err)
	}
	var sb strings.Builder
	if err := toml.NewEncoder(&sb).Encode(f); err != nil {
		return fmt.Errorf("hosted: encode session identity: %w", err)
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

// ResolveSessionPrincipal learns who the session token acts as by registering
// this checkout's location (a session-allowed route that returns the user id),
// caches the id, and returns it. It never calls /api/v1/me. c needs a location;
// when it has none one is attached from repoRoot.
func ResolveSessionPrincipal(ctx context.Context, c *Client, repoRoot, server string) (string, error) {
	cred, err := c.store.Load(c.server)
	if err != nil {
		return "", err
	}
	if !cred.IsSession() {
		return "", fmt.Errorf("hosted: %s is not set — no session token to resolve", SessionTokenEnv)
	}
	if c.location == "" {
		id, lerr := LocationID(repoRoot)
		if lerr != nil {
			return "", fmt.Errorf("hosted: checkout location: %w", lerr)
		}
		c.SetLocation(id)
	}
	userID, err := c.RegisterLocationPrincipal(ctx, LocationLabel(repoRoot))
	if err != nil {
		return "", err
	}
	if userID == "" {
		return "", fmt.Errorf("hosted: %s did not report a user id for this session token", server)
	}
	if err := saveSessionPrincipal(cred, userID); err != nil {
		return userID, err
	}
	return userID, nil
}
