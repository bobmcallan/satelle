package compact

import "testing"

func TestFoldAppliesWhenRoundTripsAndSmaller(t *testing.T) {
	orig := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	enc := "a*40" // pretend encoding
	decode := func(s string) (string, error) { return orig, nil }
	if got := Fold(orig, enc, decode); got != enc {
		t.Fatalf("Fold = %q, want the encoding %q", got, enc)
	}
}

func TestFoldRejectsWhenDecodeMismatches(t *testing.T) {
	orig := "hello world"
	enc := "wrong"
	decode := func(s string) (string, error) { return "not the original", nil }
	if got := Fold(orig, enc, decode); got != orig {
		t.Fatalf("Fold with mismatching decode = %q, want original %q", got, orig)
	}
}

func TestFoldRejectsWhenNotSmaller(t *testing.T) {
	orig := "hi"
	enc := "hi-but-longer"
	decode := func(s string) (string, error) { return orig, nil }
	if got := Fold(orig, enc, decode); got != orig {
		t.Fatalf("Fold with a bigger encoding = %q, want original %q", got, orig)
	}
}

func TestFoldRejectsOnDecodeError(t *testing.T) {
	orig := "hello world"
	enc := "x"
	decode := func(s string) (string, error) { return "", errBoom }
	if got := Fold(orig, enc, decode); got != orig {
		t.Fatalf("Fold with decode error = %q, want original %q", got, orig)
	}
}

var errBoom = &boomErr{}

type boomErr struct{}

func (*boomErr) Error() string { return "boom" }
