package remotesign

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

// The vector's public key is the one its seed derives, so the pinned signature can
// be regenerated from the seed alone.
func TestVectorPublicKeyDerivesFromItsSeed(t *testing.T) {
	v := loadVector(t)
	seed, err := base64.StdEncoding.DecodeString(v.SeedB64)
	if err != nil {
		t.Fatal(err)
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if got := base64.StdEncoding.EncodeToString(pub); got != v.PublicB64 {
		t.Errorf("public key = %s, want %s", got, v.PublicB64)
	}
}
