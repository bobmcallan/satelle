package remotesign

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type vector struct {
	SeedB64   string  `json:"seed_b64"`
	PublicB64 string  `json:"public_b64"`
	Request   Request `json:"request"`
	Nonce     string  `json:"nonce"`
	Timestamp string  `json:"timestamp"`
	Message   string  `json:"message"`
	Signature string  `json:"signature_b64"`
}

func loadVector(t *testing.T) vector {
	t.Helper()
	b, err := os.ReadFile("testdata/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The v1 message and signature are pinned by testdata/v1.json: the same bytes the
// server's SigningString builds and verifies.
func TestSignedMessageMatchesTheV1Vector(t *testing.T) {
	v := loadVector(t)
	if got := v.Request.Message(v.Nonce, v.Timestamp); got != v.Message {
		t.Fatalf("message =\n%q\nwant\n%q", got, v.Message)
	}
	if n := strings.Count(v.Message, "\n"); n != 8 || strings.HasSuffix(v.Message, "\n") {
		t.Errorf("the message is nine lines joined by newlines with no trailing one: %q", v.Message)
	}
	s, err := NewSigner(v.SeedB64)
	if err != nil {
		t.Fatal(err)
	}
	s.nonce = func() (string, error) { return v.Nonce, nil }
	ts, _ := time.Parse(time.RFC3339, "2025-10-20T00:00:00Z")
	s.now = func() time.Time { return ts }
	h, err := s.Sign(v.Request)
	if err != nil {
		t.Fatal(err)
	}
	if h.Timestamp != v.Timestamp || h.Nonce != v.Nonce || h.Signature != v.Signature {
		t.Errorf("headers = %+v, want signature %s", h, v.Signature)
	}
	pub, _ := base64.StdEncoding.DecodeString(v.PublicB64)
	sig, _ := base64.StdEncoding.DecodeString(h.Signature)
	if !ed25519.Verify(pub, []byte(v.Message), sig) {
		t.Error("the signature does not verify under the vector's public key")
	}
}

// A run end carries empty key, scope and workspace: the empty lines stay in place.
func TestRunEndMessageKeepsItsEmptyLines(t *testing.T) {
	got := Request{Method: "DELETE", Path: "/api/v1/remotes/rmt_1/runs/trg_1", TriggerID: "trg_1"}.Message("n", "1")
	want := strings.Join([]string{Version, "DELETE", "/api/v1/remotes/rmt_1/runs/trg_1", "trg_1", "", "", "", "n", "1"}, "\n")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSignerRefusesABadSeedWithoutEchoingIt(t *testing.T) {
	for _, bad := range []string{"", "not base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		_, err := NewSigner(bad)
		if err == nil {
			t.Errorf("%q must be refused", bad)
		} else if bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("the error echoes the seed: %v", err)
		}
	}
}

func TestNonceShapeAndUniqueness(t *testing.T) {
	s, err := NewSigner(loadVector(t).SeedB64)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		h, err := s.Sign(Request{Method: "POST", Path: "/p"})
		if err != nil {
			t.Fatal(err)
		}
		if len(h.Nonce) != 24 || strings.Trim(h.Nonce, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
			t.Fatalf("nonce %q is not 24 url-safe characters", h.Nonce)
		}
		if seen[h.Nonce] {
			t.Fatal("a nonce was reused")
		}
		seen[h.Nonce] = true
	}
}
