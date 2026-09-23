// Package compact implements satelle's lossless compact rendering for CLI
// output an agent pulls (sty_75b76691): arrays of uniform objects fold to a
// CSV-backed table, a unified diff patch loses its noise, and long runs of
// identical lines collapse. Every fold applies only when decoding it back
// reproduces the exact original AND the encoding is smaller (Fold /
// FoldTable) — otherwise the caller keeps printing the original.
//
// Pure mechanism: no config reads, no filenames, no opinion about which CLI
// command uses it or what counts as "noise" — that lives in internal/config
// and the CLI render path (internal/cli).
package compact

// Offloader stores long content out of line, returning its retrieval key
// (internal/retrieve.Store.Put, or an equivalent in tests).
type Offloader interface {
	Put(content []byte) (hash string, err error)
}

// Resolver resolves an offloaded hash back to its exact original bytes
// (internal/retrieve.Store.Get, or an equivalent in tests).
type Resolver interface {
	Get(hash string) (content []byte, err error)
}
