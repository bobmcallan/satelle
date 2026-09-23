package compact

// Fold returns enc when decode(enc) reproduces orig exactly and enc is
// strictly shorter than orig; otherwise it returns orig unchanged. Every
// text-based fold in this package (FoldRepeats via its caller) goes through
// this guard — a fold that cannot prove itself lossless, or that did not
// actually shrink the output, never applies (AC3).
func Fold(orig, enc string, decode func(string) (string, error)) string {
	if enc == "" || len(enc) >= len(orig) {
		return orig
	}
	got, err := decode(enc)
	if err != nil || got != orig {
		return orig
	}
	return enc
}
