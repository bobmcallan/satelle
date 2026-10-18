package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// apiPRMPath is the protected-resource metadata (RFC 9728) for the REST API
// resource. The root PRM at /.well-known/oauth-protected-resource describes the
// server's MCP resource instead and is never read here.
const apiPRMPath = "/.well-known/oauth-protected-resource/api"

// maxMetadataBytes bounds a metadata document read.
const maxMetadataBytes = 1 << 20

// endpoints is everything the login flow learns from discovery: the API
// resource to request (RFC 8707), the client id the server publishes for the
// CLI, and the authorization server's authorize and token endpoints. The binary
// holds no provider-specific value; all of it arrives here.
type endpoints struct {
	Resource              string
	ClientID              string
	AuthorizationEndpoint string
	TokenEndpoint         string
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	SatelleClientID      string   `json:"satelle_client_id"`
}

type authServerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// discover resolves server's API PRM (RFC 9728), then the first listed
// authorization server's metadata (RFC 8414, falling back to OIDC discovery).
func discover(ctx context.Context, httpClient *http.Client, server string) (endpoints, error) {
	var prm protectedResourceMetadata
	if err := getMetadata(ctx, httpClient, server+apiPRMPath, &prm); err != nil {
		return endpoints{}, fmt.Errorf("hosted: protected-resource metadata: %w", err)
	}
	switch {
	case strings.TrimSpace(prm.Resource) == "":
		return endpoints{}, errors.New("hosted: protected-resource metadata has no resource")
	case len(prm.AuthorizationServers) == 0 || strings.TrimSpace(prm.AuthorizationServers[0]) == "":
		return endpoints{}, errors.New("hosted: protected-resource metadata lists no authorization server")
	case strings.TrimSpace(prm.SatelleClientID) == "":
		return endpoints{}, errors.New("hosted: protected-resource metadata has no satelle_client_id")
	}
	issuer := strings.TrimSpace(prm.AuthorizationServers[0])
	as, err := fetchAuthServerMetadata(ctx, httpClient, issuer)
	if err != nil {
		return endpoints{}, err
	}
	return endpoints{
		Resource:              prm.Resource,
		ClientID:              prm.SatelleClientID,
		AuthorizationEndpoint: as.AuthorizationEndpoint,
		TokenEndpoint:         as.TokenEndpoint,
	}, nil
}

// fetchAuthServerMetadata reads issuer's RFC 8414 document, falling back to the
// OpenID Connect discovery document on a 404, and checks the issuer matches
// (RFC 8414 §3.3) and both endpoints are present.
func fetchAuthServerMetadata(ctx context.Context, httpClient *http.Client, issuer string) (authServerMetadata, error) {
	trimmed := strings.TrimRight(issuer, "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return authServerMetadata{}, fmt.Errorf("hosted: authorization server %q is not a URL", issuer)
	}
	// RFC 8414 §3.1 inserts the well-known segment between host and path.
	rfc8414 := u.Scheme + "://" + u.Host + "/.well-known/oauth-authorization-server" + u.Path
	var as authServerMetadata
	err = getMetadata(ctx, httpClient, rfc8414, &as)
	if errors.Is(err, errMetadataNotFound) {
		err = getMetadata(ctx, httpClient, trimmed+"/.well-known/openid-configuration", &as)
	}
	if err != nil {
		return authServerMetadata{}, fmt.Errorf("hosted: authorization server metadata: %w", err)
	}
	if strings.TrimRight(as.Issuer, "/") != trimmed {
		return authServerMetadata{}, fmt.Errorf("hosted: authorization server metadata issuer %q does not match %q", as.Issuer, issuer)
	}
	if as.AuthorizationEndpoint == "" || as.TokenEndpoint == "" {
		return authServerMetadata{}, errors.New("hosted: authorization server metadata lacks authorization_endpoint or token_endpoint")
	}
	return as, nil
}

var errMetadataNotFound = errors.New("metadata document not found (HTTP 404)")

// getMetadata GETs target and decodes a JSON document into out.
func getMetadata(ctx context.Context, httpClient *http.Client, target string, out any) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("GET %s: %w", target, errMetadataNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", target, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: decode: %w", target, err)
	}
	return nil
}
