// `satelle doctor` tuning (the context audit). The threshold below is a fact the
// operator may change, so it is configuration: an embedded default
// (substrate/config/doctor.toml) a repo overrides in satelle.toml under [doctor],
// never a number compiled into the audit.
package config

import (
	"sync"

	"github.com/BurntSushi/toml"
)

// DoctorConfig is the [doctor] table.
//
//	[doctor]
//	duplicate_min_words = 14
type DoctorConfig struct {
	// DuplicateMinWords is the shortest run of consecutive words that doctor
	// reports as verbatim-duplicated guidance. Zero or negative means unset (the
	// embedded default applies).
	DuplicateMinWords int `toml:"duplicate_min_words"`
}

const doctorFile = "substrate/config/doctor.toml"

var (
	embeddedDoctorOnce sync.Once
	embeddedDoctor     DoctorConfig
	embeddedDoctorErr  error
)

// EmbeddedDoctor returns the embedded default [doctor] table. Parsed once; the
// zero value on a parse failure (EmbeddedDoctorErr reports it, tests assert nil).
func EmbeddedDoctor() DoctorConfig {
	embeddedDoctorOnce.Do(func() {
		raw, err := substrateFS.ReadFile(doctorFile)
		if err != nil {
			embeddedDoctorErr = err
			return
		}
		var file struct {
			Doctor DoctorConfig `toml:"doctor"`
		}
		if _, err := toml.Decode(string(raw), &file); err != nil {
			embeddedDoctorErr = err
			return
		}
		embeddedDoctor = file.Doctor
	})
	return embeddedDoctor
}

// EmbeddedDoctorErr is non-nil when the embedded doctor defaults failed to load
// or parse. Exposed for tests.
func EmbeddedDoctorErr() error {
	_ = EmbeddedDoctor()
	return embeddedDoctorErr
}

// ResolveDuplicateMinWords returns the verbatim-duplication threshold: the
// repo's [doctor] duplicate_min_words, else the embedded default. It is 0 only
// when neither yields a positive value (an unreadable embed), which callers
// treat as "the check cannot run" rather than a threshold of zero.
func (c Config) ResolveDuplicateMinWords() int {
	if c.Doctor.DuplicateMinWords > 0 {
		return c.Doctor.DuplicateMinWords
	}
	return EmbeddedDoctor().DuplicateMinWords
}
