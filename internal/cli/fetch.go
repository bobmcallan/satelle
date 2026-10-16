package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// httpGetBytes is a ctx-bound GET that returns the body of a 200. accept, when
// set, is the request's Accept header; each caller owns its own. Shared by
// `satelle update` and `satelle policy refresh`. It sends no credentials.
func httpGetBytes(ctx context.Context, url, accept string) ([]byte, error) {
	return httpGetBytesAuth(ctx, url, accept, "")
}

// httpGetBytesAuth is httpGetBytes with an optional bearer token, sent only in
// the Authorization header (never in the URL or an error). Go's client drops
// Authorization on a redirect to another host, so a redirect cannot carry it.
// A rate-limited response comes back as a *rateLimitError.
func httpGetBytesAuth(ctx context.Context, url, accept, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if rl := rateLimited(url, resp); rl != nil {
			return nil, rl
		}
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// rateLimitError is a 403/429 that the server marked as a rate limit. It is
// vendor-neutral: callers that can act on it (a token for the GitHub API) wrap
// it with their own remedy.
type rateLimitError struct {
	url    string
	status string
	reset  time.Time // zero when the response gave no usable reset
}

func (e *rateLimitError) Error() string {
	msg := fmt.Sprintf("%s: rate limited (%s)", e.url, e.status)
	if !e.reset.IsZero() {
		msg += fmt.Sprintf("; resets at %s (in %s)", e.reset.Local().Format(time.RFC3339),
			time.Until(e.reset).Round(time.Second))
	}
	return msg
}

// rateLimited returns the rate-limit error for a 403 or 429 carrying
// X-RateLimit-Remaining: 0 or a Retry-After header, else nil: a bare 403 is an
// ordinary refusal and keeps its plain text.
func rateLimited(url string, resp *http.Response) *rateLimitError {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	h := resp.Header
	retryAfter := strings.TrimSpace(h.Get("Retry-After"))
	if strings.TrimSpace(h.Get("X-RateLimit-Remaining")) != "0" && retryAfter == "" {
		return nil
	}
	e := &rateLimitError{url: url, status: resp.Status}
	if secs, err := strconv.ParseInt(retryAfter, 10, 64); err == nil && secs >= 0 {
		e.reset = time.Now().Add(time.Duration(secs) * time.Second)
	} else if epoch, err := strconv.ParseInt(strings.TrimSpace(h.Get("X-RateLimit-Reset")), 10, 64); err == nil && epoch > 0 {
		e.reset = time.Unix(epoch, 0)
	}
	return e
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
