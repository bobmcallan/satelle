package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestTagchipRendersKVAndBare verifies the under-title chips: a key:value tag
// renders as a kv chip distinguishing key from value, a bare tag stays plain.
func TestTagchipRendersKVAndBare(t *testing.T) {
	now := time.Now()
	it := workitem.Item{
		ID: "sty_1", Kind: workitem.KindStory, Title: "x", Status: "open", Category: "feature",
		Tags: []string{"ui", "epic:summariser"}, CreatedAt: now, UpdatedAt: now,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "workitemRows", []rowVM{{Item: it}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `<span class="tagchip">ui</span>`) {
		t.Errorf("bare tag should render as a plain chip; got:\n%s", out)
	}
	if !strings.Contains(out, `class="tagchip kv"`) ||
		!strings.Contains(out, `<span class="k">epic</span>`) ||
		!strings.Contains(out, `<span class="v">summariser</span>`) {
		t.Errorf("kv tag should render as a kv chip with k/v spans; got:\n%s", out)
	}
	// The category renders as a distinct kv chip under the title (like satellites).
	if !strings.Contains(out, `class="tagchip kv cat"`) ||
		!strings.Contains(out, `<span class="k">category</span>`) ||
		!strings.Contains(out, `<span class="v">feature</span>`) {
		t.Errorf("category should render as a distinct kv chip; got:\n%s", out)
	}
}
