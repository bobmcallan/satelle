package remotechan

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTokenBookSingleUse(t *testing.T) {
	b := NewTokenBook(newFakeClock())
	tok, err := b.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Redeem(tok); err != nil {
		t.Fatalf("first redeem = %v", err)
	}
	if err := b.Redeem(tok); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("second redeem = %v, want ErrTokenUsed", err)
	}
	if err := b.Redeem("never-minted"); !errors.Is(err, ErrTokenUnknown) {
		t.Fatalf("unminted redeem = %v, want ErrTokenUnknown", err)
	}
}

func TestTokenBookExpires(t *testing.T) {
	clock := newFakeClock()
	b := NewTokenBook(clock)
	tok, _ := b.Mint()
	clock.Advance(EnrollTTL + time.Second)
	if err := b.Redeem(tok); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("redeem past EnrollTTL = %v, want ErrTokenExpired", err)
	}
	if err := b.Redeem(tok); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("an expired token must stay consumed, got %v", err)
	}
	fresh, _ := b.Mint()
	clock.Advance(EnrollTTL)
	if err := b.Redeem(fresh); err != nil {
		t.Fatalf("redeem exactly at EnrollTTL = %v, want accepted", err)
	}
}

func TestTokenBookKeepsOnlyHashes(t *testing.T) {
	b := NewTokenBook(nil)
	tok, _ := b.Mint()
	if len(tok) < 40 {
		t.Errorf("token %q is too short to carry 32 random bytes", tok)
	}
	for h := range b.pending {
		if strings.Contains(string(h[:]), tok) {
			t.Fatal("book holds the token itself")
		}
	}
	if b.Outstanding() != 1 {
		t.Errorf("Outstanding = %d, want 1", b.Outstanding())
	}
}

func TestTokenBookConcurrentRedeemSucceedsOnce(t *testing.T) {
	b := NewTokenBook(nil)
	tok, _ := b.Mint()
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Redeem(tok) == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d redeems succeeded, want exactly 1", won)
	}
}
