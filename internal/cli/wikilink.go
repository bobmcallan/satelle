// wikilink.go — the cli's names for the [[wikilink]] resolver.
//
// The resolver itself lives in internal/ctxaudit so `satelle validate` and
// `satelle doctor` share exactly one implementation. These are aliases onto it,
// kept so migrate and validate keep their call sites; no logic lives here.
package cli

import (
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ctxaudit"
)

var (
	wikiLinkRe            = ctxaudit.WikiLinkRe
	substrateKindsScanned = ctxaudit.SubstrateKindsScanned
)

// maskQuotedCode blanks fenced code and inline spans, length-preserving.
func maskQuotedCode(body string) string { return ctxaudit.MaskQuotedCode(body) }

// auditWikilinks returns a problem string for every dangling [[target]] under
// dataDir's substrate, constitution and embedded defaults.
func auditWikilinks(dataDir string, embedded []config.EmbeddedDefault) []string {
	return ctxaudit.AuditWikilinks(dataDir, embedded)
}
