package remotechan

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EnrollTTL is how long a minted enrollment token stays redeemable. It is also
// how long the local end waits for the remote it launched to enroll.
const EnrollTTL = 10 * time.Minute

// The refusals Redeem gives. Each is final: a refused token is never retried.
var (
	// ErrTokenUsed: the token was already redeemed (single use).
	ErrTokenUsed = errors.New("remotechan: enrollment token already used")
	// ErrTokenExpired: the token was redeemed after EnrollTTL.
	ErrTokenExpired = errors.New("remotechan: enrollment token expired")
	// ErrTokenUnknown: local satelle never minted this token.
	ErrTokenUnknown = errors.New("remotechan: enrollment token not recognised")
)

// TokenBook mints single-use, short-lived enrollment tokens and redeems them.
// It keeps only the SHA-256 of a token, never the token, so a dump of the book
// cannot enroll a remote. A token lives for one start: local satelle mints it
// when it launches the node and the node presents it once.
type TokenBook struct {
	clock Clock

	mu      sync.Mutex
	pending map[[sha256.Size]byte]time.Time // hash → expiry
	spent   map[[sha256.Size]byte]struct{}  // redeemed or expired, so a replay reads as used
}

// NewTokenBook returns an empty book on clock (nil is the real clock).
func NewTokenBook(clock Clock) *TokenBook {
	if clock == nil {
		clock = RealClock()
	}
	return &TokenBook{
		clock:   clock,
		pending: map[[sha256.Size]byte]time.Time{},
		spent:   map[[sha256.Size]byte]struct{}{},
	}
}

// Mint returns a fresh token (32 random bytes, base64url) valid for EnrollTTL.
func (b *TokenBook) Mint() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("remotechan: mint token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[sha256.Sum256([]byte(token))] = b.clock.Now().Add(EnrollTTL)
	return token, nil
}

// Redeem consumes token. The lookup and the delete are one critical section, so
// of any number of concurrent presentations exactly one succeeds. An expired
// token is consumed too: it never becomes redeemable again.
func (b *TokenBook) Redeem(token string) error {
	h := sha256.Sum256([]byte(token))
	b.mu.Lock()
	defer b.mu.Unlock()
	expiry, ok := b.pending[h]
	if !ok {
		if _, was := b.spent[h]; was {
			return ErrTokenUsed
		}
		return ErrTokenUnknown
	}
	delete(b.pending, h)
	b.spent[h] = struct{}{}
	if b.clock.Now().After(expiry) {
		return ErrTokenExpired
	}
	return nil
}

// Outstanding is how many minted tokens are still unredeemed.
func (b *TokenBook) Outstanding() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
