package hosted

// Checkout location identity (epic:story-checkout, sty_e88d77ce).
//
// A location is one machine × repo-root checkout. The id is derived, persisted
// per-user outside any repo (beside credentials.toml), registered once per
// hosted server, and sent as x-satelle-location on authenticated sync/story
// calls. Unbound repos never touch this file.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LocationHeader is the REST header / gRPC metadata key a bound checkout sends.
// Same string on both transports (satelle-server LocationHeader).
const LocationHeader = "x-satelle-location"

// LocationStatePathOverride, when non-empty, replaces LocationStatePath (tests).
var LocationStatePathOverride string

var locationMu sync.Mutex

type locationEntry struct {
	ID         string          `json:"id"`
	Registered map[string]bool `json:"registered,omitempty"`
}

type locationStateFile struct {
	Locations       map[string]locationEntry `json:"locations"`
	MachineFallback string                   `json:"machine_fallback,omitempty"`
}

// LocationStatePath is the per-user JSON file holding checkout ids.
// Same directory as credentials.toml; file location-state.json.
func LocationStatePath() (string, error) {
	if p := strings.TrimSpace(LocationStatePathOverride); p != "" {
		return p, nil
	}
	cred, err := CredentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cred), "location-state.json"), nil
}

// ValidLocationID reports whether id meets the server contract: 8–128 chars of
// [A-Za-z0-9._:-] after trim.
func ValidLocationID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == ':' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func locationRepoKey(repoRoot string) string {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	return filepath.Clean(abs)
}

func deriveLocationID(machine, repoRoot string) string {
	sum := sha256.Sum256([]byte(machine + "\x00" + locationRepoKey(repoRoot)))
	return "loc_" + hex.EncodeToString(sum[:])[:32]
}

func readMachineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	if h, err := os.Hostname(); err == nil {
		if s := strings.TrimSpace(h); s != "" {
			return s
		}
	}
	return ""
}

func randomFallbackID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("hosted: location fallback: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func loadLocationState() (locationStateFile, error) {
	path, err := LocationStatePath()
	if err != nil {
		return locationStateFile{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return locationStateFile{Locations: map[string]locationEntry{}}, nil
		}
		return locationStateFile{}, fmt.Errorf("hosted: read location state: %w", err)
	}
	var state locationStateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return locationStateFile{}, fmt.Errorf("hosted: decode location state: %w", err)
	}
	if state.Locations == nil {
		state.Locations = map[string]locationEntry{}
	}
	return state, nil
}

func writeLocationState(state locationStateFile) error {
	path, err := LocationStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("hosted: create location state dir: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("hosted: encode location state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("hosted: write location state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosted: rename location state: %w", err)
	}
	return nil
}

func machineIDLocked(state *locationStateFile) (string, error) {
	if m := readMachineID(); m != "" {
		return m, nil
	}
	if s := strings.TrimSpace(state.MachineFallback); s != "" {
		return s, nil
	}
	fb, err := randomFallbackID()
	if err != nil {
		return "", err
	}
	state.MachineFallback = fb
	return fb, nil
}

// LocationID returns the persisted checkout id for repoRoot, deriving and
// writing one if missing. A persisted id that fails ValidLocationID is
// re-derived. Never returns an empty id on success.
func LocationID(repoRoot string) (string, error) {
	locationMu.Lock()
	defer locationMu.Unlock()
	state, err := loadLocationState()
	if err != nil {
		return "", err
	}
	key := locationRepoKey(repoRoot)
	if e, ok := state.Locations[key]; ok && ValidLocationID(e.ID) {
		return e.ID, nil
	}
	machine, err := machineIDLocked(&state)
	if err != nil {
		return "", err
	}
	id := deriveLocationID(machine, repoRoot)
	if !ValidLocationID(id) {
		return "", fmt.Errorf("hosted: derived location id %q fails the server contract", id)
	}
	// A replacement id (missing or invalid persisted) is unregistered.
	state.Locations[key] = locationEntry{ID: id, Registered: map[string]bool{}}
	if err := writeLocationState(state); err != nil {
		return "", err
	}
	return id, nil
}

// LocationRegistered reports whether repoRoot's id has been registered on server.
func LocationRegistered(repoRoot, server string) bool {
	locationMu.Lock()
	defer locationMu.Unlock()
	state, err := loadLocationState()
	if err != nil {
		return false
	}
	e, ok := state.Locations[locationRepoKey(repoRoot)]
	if !ok || e.Registered == nil {
		return false
	}
	return e.Registered[normalizeServerURL(server)]
}

// MarkLocationRegistered records a successful POST /api/v1/locations for
// this repoRoot × server pair.
func MarkLocationRegistered(repoRoot, server string) error {
	locationMu.Lock()
	defer locationMu.Unlock()
	state, err := loadLocationState()
	if err != nil {
		return err
	}
	key := locationRepoKey(repoRoot)
	e := state.Locations[key]
	if e.Registered == nil {
		e.Registered = map[string]bool{}
	}
	e.Registered[normalizeServerURL(server)] = true
	state.Locations[key] = e
	return writeLocationState(state)
}

// LocationLabel is the human-readable register payload: hostname + repo path.
func LocationLabel(repoRoot string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	return strings.TrimSpace(host) + " " + locationRepoKey(repoRoot)
}
