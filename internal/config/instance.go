package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// InstanceID returns a stable short identity for a satelle home directory
// (GlobalDir). Serve exposes it on /healthz as X-Satelle-Instance so CLI
// auto-bootstrap only seeds a serve that belongs to the same home
// (sty_5aa08259 / epic:mirror-hygiene) — hermetic SATELLE_HOME runs cannot
// silently adopt the operator's live :8787 serve.
func InstanceID(globalDir string) string {
	abs, err := filepath.Abs(strings.TrimSpace(globalDir))
	if err != nil || abs == "" {
		abs = strings.TrimSpace(globalDir)
	}
	if abs == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return hex.EncodeToString(sum[:8])
}

// CurrentInstanceID is InstanceID(GlobalDir()).
func CurrentInstanceID() string {
	return InstanceID(GlobalDir())
}

// SafeCurrentInstanceID is CurrentInstanceID but returns "" under go test when
// SATELLE_HOME is unset — so NewMirror in unit tests does not panic on
// GlobalDir's isolation fence (sty_c36c211f / sty_5aa08259).
func SafeCurrentInstanceID() string {
	if testing.Testing() && strings.TrimSpace(os.Getenv("SATELLE_HOME")) == "" {
		return ""
	}
	return CurrentInstanceID()
}
