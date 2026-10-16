package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpGetBytes is a ctx-bound GET that returns the body of a 200. accept, when
// set, is the request's Accept header; each caller owns its own. Shared by
// `satelle update` and `satelle policy refresh`.
func httpGetBytes(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum checks data against the sha256 in shaLine ("<hex>  <name>").
func verifyChecksum(data []byte, shaLine string) error {
	want := parseChecksumLine(shaLine)
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("sha256 mismatch (want %s, got %s)", want, got)
	}
	return nil
}

// parseChecksumLine extracts the leading hex field from a sha256 line
// ("<hex>  <name>"). Shared by verifyChecksum and publishedChecksum.
func parseChecksumLine(shaLine string) string {
	want := strings.TrimSpace(shaLine)
	if i := strings.IndexAny(want, " \t"); i > 0 {
		want = want[:i]
	}
	return want
}
