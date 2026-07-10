package cli

// Local adoption records for published team artifacts (epic:sync-publish).
// Stored under the repo data dir so publish check can detect newer versions.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const adoptionsFile = "adoptions.json"

type adoptionRecord struct {
	Workspace   string `json:"workspace"`
	Path        string `json:"path"`
	Version     int    `json:"version"`
	Kind        string `json:"kind,omitempty"`
	PublisherID string `json:"publisher_id,omitempty"`
}

var adoptionMu sync.Mutex

func adoptionsPath(dataDir string) string {
	return filepath.Join(dataDir, adoptionsFile)
}

func loadAdoptions(dataDir string) ([]adoptionRecord, error) {
	adoptionMu.Lock()
	defer adoptionMu.Unlock()
	b, err := os.ReadFile(adoptionsPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read adoptions: %w", err)
	}
	var recs []adoptionRecord
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("parse adoptions: %w", err)
	}
	return recs, nil
}

func saveAdoption(dataDir string, rec adoptionRecord) error {
	adoptionMu.Lock()
	defer adoptionMu.Unlock()
	path := adoptionsPath(dataDir)
	var recs []adoptionRecord
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &recs)
	}
	found := false
	for i, r := range recs {
		if r.Workspace == rec.Workspace && r.Path == rec.Path {
			recs[i] = rec
			found = true
			break
		}
	}
	if !found {
		recs = append(recs, rec)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
