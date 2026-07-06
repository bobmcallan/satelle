// Package api provides reference wire types for the satelle hosted API.
//
// Servers in any language implement these shapes; the OpenAPI spec
// (openapi.yaml) is the canonical interface definition. This package is a
// standalone reference — it does not import internal/hosted, and the
// internal client types are structurally identical but independently owned.
package api

// Principal is the identity returned by GET /api/v1/me.
type Principal struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Project is a hosted project as returned by the projects API.
// Role is only populated by responses that carry the caller's membership.
type Project struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// CreateProjectRequest is the JSON body for POST /api/v1/projects.
type CreateProjectRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// TokenResponse is the /oauth/token success body (authorization_code and
// refresh_token grants).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// OAuthError is the /oauth/token error body ({"error","error_description"}).
type OAuthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

// ErrorEnvelope is the standard JSON error body returned by API endpoints.
type ErrorEnvelope struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
