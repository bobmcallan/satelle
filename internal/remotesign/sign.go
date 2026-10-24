// Package remotesign signs the requests a remote node makes to satelle.dev with
// the remote's Ed25519 key (sty_0b1c72a9). It is the node's half of the server's
// signed-read contract: the message is the version, the request method and path,
// then the trigger id, key, scope, workspace, nonce and timestamp, one per line
// and with no trailing newline. The server verifies exactly that string, so a
// change here is a change to the wire contract and testdata/v1.json pins it.
//
// The private key is a 32-byte seed. It is held by the host's secret store, handed
// to the node as the standard base64 of the seed, and never written by satelle.
package remotesign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Version starts every signed message, so a signature made for another purpose
// never verifies as a read.
const Version = "satelle-remote-read-v1"

// The three headers a signed request carries.
const (
	HeaderTimestamp = "X-Satelle-Timestamp"
	HeaderNonce     = "X-Satelle-Nonce"
	HeaderSignature = "X-Satelle-Signature"
)

// Request is the part of a signed request the message covers. For a run's end or
// refresh Key, Scope and Workspace are empty.
type Request struct {
	Method, Path, TriggerID string
	Key, Scope, Workspace   string
}

// Message is the exact string the remote signs for req.
func (r Request) Message(nonce, timestamp string) string {
	return strings.Join([]string{Version, r.Method, r.Path, r.TriggerID, r.Key, r.Scope, r.Workspace, nonce, timestamp}, "\n")
}

// Headers are the signature headers of one signed request.
type Headers struct {
	Timestamp, Nonce, Signature string
}

// Map is the headers as the transport sets them.
func (h Headers) Map() map[string]string {
	return map[string]string{HeaderTimestamp: h.Timestamp, HeaderNonce: h.Nonce, HeaderSignature: h.Signature}
}

// Signer signs requests with one remote's private key.
type Signer struct {
	key ed25519.PrivateKey
	now func() time.Time
	// nonce returns a fresh nonce; tests replace it.
	nonce func() (string, error)
}

// NewSigner returns the Signer for a seed given as the standard base64 of its 32
// bytes. An error never carries the seed.
func NewSigner(seedB64 string) (*Signer, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedB64))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("the remote's private key is not the standard base64 of a %d-byte Ed25519 seed", ed25519.SeedSize)
	}
	return &Signer{key: ed25519.NewKeyFromSeed(seed), now: time.Now, nonce: newNonce}, nil
}

// Sign signs req with a fresh nonce and the current time.
func (s *Signer) Sign(req Request) (Headers, error) {
	nonce, err := s.nonce()
	if err != nil {
		return Headers{}, err
	}
	ts := strconv.FormatInt(s.now().Unix(), 10)
	sig := ed25519.Sign(s.key, []byte(req.Message(nonce, ts)))
	return Headers{Timestamp: ts, Nonce: nonce, Signature: base64.StdEncoding.EncodeToString(sig)}, nil
}

// newNonce is 24 url-safe characters from 18 random bytes; the server wants 16
// to 128 of [A-Za-z0-9_-].
func newNonce() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
