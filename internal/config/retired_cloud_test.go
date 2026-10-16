package config

import (
	"strings"
	"testing"
)

// A cloud (or launch) table is retired schema (sty_c00782a3): every loader that
// could once have read one refuses it, saying satelle no longer manages cloud
// identity and naming the table to delete.
func TestRetiredCloudTablesAreRefused(t *testing.T) {
	cases := []struct {
		name  string
		load  func() error
		table string
		file  string
	}{
		{"catalog cloud", func() error {
			_, err := ParseGlobalAgents("[profiles.ops.cloud.sample]\nkey = \"x\"\n")
			return err
		}, "[profiles.ops.cloud]", "~/.satelle/agents.toml"},
		{"catalog launch", func() error {
			_, err := ParseGlobalAgents("[profiles.ops]\ncommand = \"claude\"\n\n[profiles.ops.launch]\nsteps = \"x\"\n")
			return err
		}, "[profiles.ops.launch]", "~/.satelle/agents.toml"},
		{"repo executor cloud", func() error {
			_, err := loadAgentsBody("[executor]\ncommand = \"in-loop\"\n\n[executor.cloud.sample]\nkey = \"x\"\n")
			return err
		}, "[executor.cloud]", ".satelle/workflows/agents.toml"},
		{"repo reviewer cloud", func() error {
			_, err := loadAgentsBody("[reviewer]\ncommand = \"claude\"\n\n[reviewer.cloud]\nkey = \"x\"\n")
			return err
		}, "[reviewer.cloud]", ".satelle/workflows/agents.toml"},
		{"repo custom binding cloud", func() error {
			_, err := loadAgentsBody("[coder]\ncommand = \"claude\"\n\n[coder.cloud]\nkey = \"x\"\n")
			return err
		}, "[coder.cloud]", ".satelle/workflows/agents.toml"},
		{"repo custom binding launch", func() error {
			_, err := loadAgentsBody("[coder]\ncommand = \"claude\"\n\n[coder.launch]\nsteps = \"x\"\n")
			return err
		}, "[coder.launch]", ".satelle/workflows/agents.toml"},
		{"legacy nested cloud", func() error {
			_, _, err := MigrateAgents("[agents.coder]\ncommand = \"claude\"\n\n[agents.coder.cloud]\nkey = \"x\"\n")
			return err
		}, "[agents.coder.cloud]", ".satelle/workflows/agents.toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.load()
			if err == nil {
				t.Fatal("a retired table loaded")
			}
			for _, want := range []string{CloudIdentityRetired, "no longer manages cloud identity", "delete the " + tc.table + " table from " + tc.file} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

// The refusal reads decodeAgents' own classification: [defaults] is not a
// binding, so a key of that name there is not the retired table.
func TestRetiredCloudRefusalIgnoresDefaults(t *testing.T) {
	_, err := loadAgentsBody("[defaults]\ncloud = \"x\"\n\n[executor]\ncommand = \"in-loop\"\n")
	if err != nil && strings.Contains(err.Error(), CloudIdentityRetired) {
		t.Errorf("[defaults] cloud was refused as the retired table: %v", err)
	}
}
