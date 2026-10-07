package hosted

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAreaBaseRoundTripKeyedPerAreaProjectAndRepo(t *testing.T) {
	DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "state.json")
	t.Cleanup(func() { DocumentSyncStatePathOverride = "" })

	if _, ok, err := LoadAreaBase("https://s", "p", "/repo", "skills"); err != nil || ok {
		t.Fatalf("fresh state: ok=%v err=%v, want no base", ok, err)
	}
	want := AreaBase{Version: 3, Files: map[string]string{"skills/a.md": "aa"}, Unmerged: map[string]string{"skills/a.md": "rr"}}
	if err := SaveAreaBase("https://s", "p", "/repo", "skills", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadAreaBase("https://s", "p", "/repo", "skills")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v ok=%v err=%v, want %+v", got, ok, err, want)
	}
	for _, other := range [][4]string{
		{"https://s", "p", "/repo", "documents"}, // another area
		{"https://s", "q", "/repo", "skills"},    // another project
		{"https://s", "p", "/other", "skills"},   // another checkout
	} {
		if _, ok, _ := LoadAreaBase(other[0], other[1], other[2], other[3]); ok {
			t.Errorf("base leaked to %v", other)
		}
	}
	// The other state this file holds is untouched by a base write.
	if err := SaveDocumentCursor("https://s", "p", "/repo", "c1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadAreaBase("https://s", "p", "/repo", "skills"); !ok {
		t.Error("a cursor write dropped the base")
	}
}
