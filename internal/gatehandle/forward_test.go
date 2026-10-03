package gatehandle

import "testing"

func TestForwardsAreKeyedBySession(t *testing.T) {
	serve := New(t.TempDir())
	if got := serve.Forwards("s1"); len(got) != 0 {
		t.Fatalf("a fresh store has pointers: %v", got)
	}
	if err := serve.Forward("s1", "gw_aaaaaaaaaa", "/rt/b"); err != nil {
		t.Fatal(err)
	}
	if err := serve.Forward("s1", "gw_bbbbbbbbbb", "/rt/c"); err != nil {
		t.Fatal(err)
	}
	if err := serve.Forward("s2", "gw_cccccccccc", "/rt/b"); err != nil {
		t.Fatal(err)
	}
	got := serve.Forwards("s1")
	if len(got) != 2 {
		t.Fatalf("s1 pointers = %v, want 2", got)
	}
	for _, f := range got {
		if f.ID == "gw_cccccccccc" {
			t.Errorf("s1 sees s2's pointer: %v", got)
		}
	}
	serve.Unforward("s1", "gw_aaaaaaaaaa")
	if got := serve.Forwards("s1"); len(got) != 1 || got[0].ID != "gw_bbbbbbbbbb" || got[0].RuntimeDir != "/rt/c" {
		t.Errorf("after Unforward = %v", got)
	}
	// Pointers are not handles: they never show up as undelivered runs.
	if ids := serve.Undelivered(); len(ids) != 0 {
		t.Errorf("a pointer was listed as a handle: %v", ids)
	}
}

func TestStoreRemembersItsRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	if got := New(dir).RuntimeDir(); got != dir {
		t.Errorf("RuntimeDir = %q, want %q", got, dir)
	}
}
