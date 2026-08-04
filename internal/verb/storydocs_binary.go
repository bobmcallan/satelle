package verb

// Binary story attachments (sty_40e5a305): additive path beside markdown
// attachments. Bytes are stored verbatim with a .satelle.json sidecar for
// metadata (frontmatter cannot be prepended into a PNG). Cap, allowlist, and
// sniff/extension/declared-type cross-check all live HERE so the CLI and any
// future hosted/MCP caller share one rule.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{Name: "story-doc-attach-binary", Description: "Attach a binary document to a story (base64 body, sidecar metadata)", Invoke: storyDocAttachBinary})
	Register(&Verb{Name: "story-doc-get-binary", Description: "Read a binary story attachment as base64 + metadata", Invoke: storyDocGetBinary})
}

// SidecarSuffix is the reserved metadata suffix for binary attachments.
// An attachment name ending in this suffix is rejected so callers cannot forge
// sidecar metadata for another file.
const SidecarSuffix = ".satelle.json"

// package-level attachment policy (wired by SetAttachmentPolicy from the CLI
// bootstrap). Zero max / empty allowlist resolve to config defaults so unit
// tests that never call SetAttachmentPolicy still get the shipped bounds.
var (
	attachMaxBytes   int64
	attachAllowTypes []string
)

// SetAttachmentPolicy wires the size cap and content-type allowlist into the
// verb package. Empty/zero values fall back to config defaults at check time.
func SetAttachmentPolicy(maxBytes int64, allowTypes []string) {
	attachMaxBytes = maxBytes
	attachAllowTypes = append([]string(nil), allowTypes...)
}

func effectiveMaxBytes() int64 {
	if attachMaxBytes > 0 {
		return attachMaxBytes
	}
	return config.DefaultAttachmentMaxBytes
}

func effectiveAllowTypes() map[string]bool {
	list := attachAllowTypes
	if len(list) == 0 {
		list = config.DefaultAttachmentAllowTypes
	}
	m := make(map[string]bool, len(list))
	for _, t := range list {
		if s := normalizeContentType(t); s != "" {
			m[s] = true
		}
	}
	return m
}

// binarySidecar is the on-disk metadata for a binary attachment. Written as
// <name.ext>.satelle.json beside the raw file.
type binarySidecar struct {
	Story       string `json:"story"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	AttachedAt  string `json:"attached_at"`
	Binary      bool   `json:"binary"`
}

type docAttachBinaryReq struct {
	StoryID     string `json:"story_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ContentType string `json:"content_type"`
	DataB64     string `json:"data_base64"`
}

type docBinaryRef struct {
	StoryID     string `json:"story_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Binary      bool   `json:"binary"`
	// DataB64 is only populated by story-doc-get-binary.
	DataB64 string `json:"data_base64,omitempty"`
}

func storyDocAttachBinary(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req docAttachBinaryReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	item, err := store.Get(ctx, req.StoryID)
	if err != nil {
		return nil, fmt.Errorf("verb: attach-binary: %s: %w", req.StoryID, err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.DataB64))
	if err != nil {
		return nil, fmt.Errorf("verb: attach-binary: data_base64: %w", err)
	}
	ref, err := writeAttachedBinary(ctx, item, req.Name, req.Type, req.ContentType, data, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(ref)
}

func storyDocGetBinary(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req docRef
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	dir, err := resolveAttachmentDir(ctx, store, req.StoryID)
	if err != nil {
		return nil, err
	}
	file := safeBinaryName(req.Name)
	if file == "" {
		return nil, fmt.Errorf("verb: doc-binary: a valid binary document name is required")
	}
	sc, err := readBinarySidecar(dir, file)
	if err != nil {
		return nil, fmt.Errorf("verb: doc-binary %s/%s: %w", req.StoryID, req.Name, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil, fmt.Errorf("verb: doc-binary %s/%s: %w", req.StoryID, req.Name, err)
	}
	return json.Marshal(docBinaryRef{
		StoryID:     req.StoryID,
		Name:        sc.Name,
		Type:        sc.Type,
		ContentType: sc.ContentType,
		Size:        sc.Size,
		SHA256:      sc.SHA256,
		Binary:      true,
		DataB64:     base64.StdEncoding.EncodeToString(data),
	})
}

// writeAttachedBinary materialises a binary attachment: raw bytes to disk
// (no frontmatter), then a sidecar with metadata, then a ledger row.
func writeAttachedBinary(ctx context.Context, item workitem.Item, name, typ, contentType string, data []byte, now time.Time) (docBinaryRef, error) {
	file := safeBinaryName(name)
	if file == "" {
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: a valid binary document name is required (extension preserved; path traversal and %s names rejected)", SidecarSuffix)
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		typ = "document"
	}
	max := effectiveMaxBytes()
	if int64(len(data)) > max {
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: too large: %d bytes exceeds cap of %d bytes", len(data), max)
	}
	ct, err := resolveAndCheckContentType(file, contentType, data)
	if err != nil {
		return docBinaryRef{}, err
	}

	dir := attachmentDir(item)
	if dir == "" {
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: attachment dir for %s (%s) not configured", item.ID, item.Kind)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: %w", err)
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	// File first, then sidecar: a partial write must not read as healthy.
	if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: %w", err)
	}
	sc := binarySidecar{
		Story:       item.ID,
		Name:        file,
		Type:        typ,
		ContentType: ct,
		Size:        int64(len(data)),
		SHA256:      digest,
		AttachedAt:  now.UTC().Format(time.RFC3339),
		Binary:      true,
	}
	sb, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		_ = os.Remove(filepath.Join(dir, file))
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: sidecar: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file+SidecarSuffix), sb, 0o644); err != nil {
		_ = os.Remove(filepath.Join(dir, file))
		return docBinaryRef{}, fmt.Errorf("verb: attach-binary: sidecar: %w", err)
	}
	appendLedger(ctx, item.ID, KindStoryDocAttached,
		fmt.Sprintf("attached %s binary %q (%s, %d bytes, sha256:%s)", typ, file, ct, len(data), digest), now)
	return docBinaryRef{
		StoryID:     item.ID,
		Name:        file,
		Type:        typ,
		ContentType: ct,
		Size:        int64(len(data)),
		SHA256:      digest,
		Binary:      true,
	}, nil
}

// resolveAndCheckContentType enforces allowlist + sniff/extension/declared
// cross-check in the verb (architecture review: not CLI-only).
func resolveAndCheckContentType(file, declared string, data []byte) (string, error) {
	declared = normalizeContentType(declared)
	extType := normalizeContentType(mime.TypeByExtension(filepath.Ext(file)))
	sniffed := normalizeContentType(http.DetectContentType(data))

	for _, t := range []string{declared, extType, sniffed} {
		if isHostileContentType(t) {
			return "", fmt.Errorf("verb: attach-binary: unsupported_type: %s is executable if served (SVG/HTML denied)", t)
		}
	}

	// Effective type: declared wins when set; else extension; else sniff.
	effective := declared
	if effective == "" {
		effective = extType
	}
	if effective == "" || effective == "application/octet-stream" {
		if sniffed != "" && sniffed != "application/octet-stream" {
			effective = sniffed
		} else if effective == "" {
			effective = "application/octet-stream"
		}
	}

	allow := effectiveAllowTypes()
	if !allow[effective] {
		return "", fmt.Errorf("verb: attach-binary: unsupported_type: %q is not in the allowlist", effective)
	}

	// Cross-check: when sniff is a concrete (non-octet-stream) type it must
	// agree with declared (if set) and with the extension type (if known).
	if sniffed != "" && sniffed != "application/octet-stream" {
		if !allow[sniffed] {
			// Sniffed something disallowed (e.g. text/plain HTML-ish, svg).
			if isHostileContentType(sniffed) {
				return "", fmt.Errorf("verb: attach-binary: unsupported_type: sniffed %s is executable if served (SVG/HTML denied)", sniffed)
			}
			return "", fmt.Errorf("verb: attach-binary: unsupported_type: sniffed content type %q is not in the allowlist", sniffed)
		}
		if declared != "" && !contentTypesCompatible(declared, sniffed) {
			return "", fmt.Errorf("verb: attach-binary: content type mismatch: declared %q but bytes sniff as %q", declared, sniffed)
		}
		if extType != "" && extType != "application/octet-stream" && !contentTypesCompatible(extType, sniffed) {
			return "", fmt.Errorf("verb: attach-binary: content type mismatch: name extension implies %q but bytes sniff as %q", extType, sniffed)
		}
	}
	return effective, nil
}

func normalizeContentType(ct string) string {
	ct = strings.TrimSpace(strings.ToLower(ct))
	if ct == "" {
		return ""
	}
	// Drop parameters (charset=…).
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

func isHostileContentType(ct string) bool {
	switch normalizeContentType(ct) {
	case "image/svg+xml", "text/html", "application/xhtml+xml", "text/xml", "application/xml":
		// text/xml and application/xml catch SVG that sniffs without the svg MIME.
		return true
	}
	return false
}

func contentTypesCompatible(a, b string) bool {
	a, b = normalizeContentType(a), normalizeContentType(b)
	if a == "" || b == "" {
		return true
	}
	if a == b {
		return true
	}
	if a == "application/octet-stream" || b == "application/octet-stream" {
		return true
	}
	// JPEG aliases.
	if (a == "image/jpeg" || a == "image/jpg") && (b == "image/jpeg" || b == "image/jpg") {
		return true
	}
	return false
}

// safeBinaryName reduces a binary attachment name to a bare filename with its
// extension preserved. Rejects traversal, embedded separators (so a/b.png is
// not silently rewritten to b.png), leading-dot names, and the reserved
// sidecar suffix.
func safeBinaryName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	// Reject any path form before Base would strip it (sty_40e5a305 AC1).
	if strings.ContainsAny(trimmed, `/\`) || filepath.IsAbs(trimmed) {
		return ""
	}
	n := baseSafe(trimmed)
	if n == "" {
		return ""
	}
	if strings.HasPrefix(n, ".") {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(n), SidecarSuffix) {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(n))
	if ext == "" {
		return ""
	}
	// Markdown namespace is owned by writeAttachedDoc / ItemDocs' .md walk.
	// A binary named *.md would land in that walk with Binary:false and
	// poison fillPayloadDocs (sty_40e5a305 AC4/AC5 code-ac-review).
	if ext == ".md" {
		return ""
	}
	return n
}

func sidecarPath(dir, file string) string {
	return filepath.Join(dir, file+SidecarSuffix)
}

func readBinarySidecar(dir, file string) (binarySidecar, error) {
	data, err := os.ReadFile(sidecarPath(dir, file))
	if err != nil {
		return binarySidecar{}, err
	}
	var sc binarySidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return binarySidecar{}, fmt.Errorf("sidecar: %w", err)
	}
	if sc.Name == "" {
		sc.Name = file
	}
	sc.Binary = true
	return sc, nil
}

// listBinaryDocs walks sidecars in dir and returns healthy binary attachments
// (sidecar present AND the data file present). Lone halves are skipped.
func listBinaryDocs(storyID, dir string) []docRef {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []docRef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), SidecarSuffix) {
			continue
		}
		file := strings.TrimSuffix(e.Name(), SidecarSuffix)
		if file == "" {
			continue
		}
		// Both halves required.
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			continue
		}
		sc, err := readBinarySidecar(dir, file)
		if err != nil {
			continue
		}
		out = append(out, docRef{
			StoryID:     storyID,
			Name:        sc.Name,
			Type:        sc.Type,
			ContentType: sc.ContentType,
			Size:        sc.Size,
			SHA256:      sc.SHA256,
			Binary:      true,
		})
	}
	return out
}

// isBinaryAttachment reports whether name resolves to a binary attachment
// (sidecar present) in dir.
func isBinaryAttachment(dir, name string) (binarySidecar, bool) {
	file := safeBinaryName(name)
	if file == "" {
		// Also try as-is for names that already include extension via safeBinaryName failure paths.
		file = baseSafe(name)
	}
	if file == "" {
		return binarySidecar{}, false
	}
	sc, err := readBinarySidecar(dir, file)
	if err != nil {
		return binarySidecar{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
		return binarySidecar{}, false
	}
	return sc, true
}

// binaryDocBodies returns reference-only DocBody entries for binary attachments
// (empty Body, Binary true) so fillPayloadDocs can list them without inlining bytes.
func binaryDocBodies(dir string) []DocBody {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []DocBody
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), SidecarSuffix) {
			continue
		}
		file := strings.TrimSuffix(e.Name(), SidecarSuffix)
		if file == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			continue
		}
		sc, err := readBinarySidecar(dir, file)
		if err != nil {
			continue
		}
		out = append(out, DocBody{
			Name:        sc.Name,
			Type:        sc.Type,
			Body:        "", // never inline binary
			Binary:      true,
			ContentType: sc.ContentType,
			Size:        sc.Size,
			SHA256:      sc.SHA256,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
