package api_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/api"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// TestJSONKeyIdentity verifies that api reference types marshal to the same
// JSON field names as the real internal/hosted client types, proving the
// published shapes match what the CLI actually sends/receives.
func TestJSONKeyIdentity(t *testing.T) {
	t.Run("Principal", func(t *testing.T) {
		h := hosted.Principal{ID: "a", Email: "b", DisplayName: "c", Role: "d"}
		r := api.Principal{ID: "a", Email: "b", DisplayName: "c", Role: "d"}
		assertByteIdenticalJSON(t, h, r)
	})
	t.Run("Project", func(t *testing.T) {
		h := hosted.Project{ID: "a", Slug: "b", Name: "c", Role: "d"}
		r := api.Project{ID: "a", Slug: "b", Name: "c", Role: "d"}
		assertByteIdenticalJSON(t, h, r)
	})
	t.Run("Workspace", func(t *testing.T) {
		h := hosted.Workspace{ID: "a", Kind: "team", Name: "Acme Team"}
		r := api.Workspace{ID: "a", Kind: "team", Name: "Acme Team"}
		assertByteIdenticalJSON(t, h, r)
	})
	t.Run("TokenResponse", func(t *testing.T) {
		// hosted.tokenResponse is unexported, so marshal a hosted token flow
		// result indirectly: compare field names via api.TokenResponse.
		tr := api.TokenResponse{
			AccessToken: "at", RefreshToken: "rt",
			TokenType: "Bearer", ExpiresIn: 3600, Scope: "read",
		}
		b, err := json.Marshal(tr)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"access_token", "refresh_token", "token_type", "expires_in", "scope"} {
			if !strings.Contains(string(b), key) {
				t.Errorf("TokenResponse JSON missing key %q: got %s", key, b)
			}
		}
	})
}

// assertByteIdenticalJSON marshals two values (one from hosted, one from api)
// and asserts the JSON output is byte-identical.
func assertByteIdenticalJSON(t *testing.T, hostedVal, apiVal any) {
	t.Helper()
	hBytes, err := json.Marshal(hostedVal)
	if err != nil {
		t.Fatalf("marshal hosted value: %v", err)
	}
	rBytes, err := json.Marshal(apiVal)
	if err != nil {
		t.Fatalf("marshal api value: %v", err)
	}
	if string(hBytes) != string(rBytes) {
		t.Errorf("JSON mismatch:\n  hosted: %s\n  api:    %s", hBytes, rBytes)
	}
}

// TestOAuthErrorFieldNames verifies OAuthError marshals with the correct
// JSON keys (error, error_description) matching the /oauth/token spec.
func TestOAuthErrorFieldNames(t *testing.T) {
	oe := api.OAuthError{Code: "invalid_grant", Description: "bad token"}
	b, err := json.Marshal(oe)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"error"`, `"error_description"`} {
		if !strings.Contains(s, key) {
			t.Errorf("OAuthError JSON missing key %q: got %s", key, s)
		}
	}
}

// TestErrorEnvelopeFieldNames verifies ErrorEnvelope marshals with the
// correct JSON keys matching the server error response shape.
func TestErrorEnvelopeFieldNames(t *testing.T) {
	env := api.ErrorEnvelope{Error: "not_found", Message: "project does not exist"}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"error"`, `"message"`} {
		if !strings.Contains(s, key) {
			t.Errorf("ErrorEnvelope JSON missing key %q: got %s", key, s)
		}
	}
}

// TestOpenAPISchemaCoverage verifies every type exported by the api package
// has a matching schema component in openapi.yaml.
func TestOpenAPISchemaCoverage(t *testing.T) {
	yamlBytes, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	yaml := string(yamlBytes)

	// Each exported struct in api/ must have a corresponding component schema.
	exportedSchemas := []string{
		"Principal",
		"Project",
		"Workspace",
		"CreateProjectRequest",
		"TokenResponse",
		"OAuthError",
		"ErrorEnvelope",
	}
	for _, name := range exportedSchemas {
		if !strings.Contains(yaml, name) {
			t.Errorf("openapi.yaml missing schema component %q", name)
		}
	}
}

// TestNoCrossImports verifies that internal/ does not import api/ (production
// code must be independent; the test importing both sides is fine).
func TestNoCrossImports(t *testing.T) {
	entries, err := os.ReadDir("../internal")
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir("../internal/" + e.Name())
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".go") {
				continue
			}
			path := "../internal/" + e.Name() + "/" + f.Name()
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if strings.Contains(string(content), "github.com/bobmcallan/satelle/api") {
				t.Errorf("internal package %s/%s imports api/ — cross-import forbidden", e.Name(), f.Name())
			}
		}
	}
}

// TestNoSubstrateInSpec verifies the retired substrate-backup interface is not
// published in the OpenAPI spec.
func TestNoSubstrateInSpec(t *testing.T) {
	yamlBytes, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	yaml := string(yamlBytes)

	if strings.Contains(yaml, "substrate") {
		t.Error("openapi.yaml contains 'substrate' — retired interface must not be published")
	}
}
