// Where an imported OAuth2 collection puts its access token.
//
// Postman writes `addTokenTo` into a collection only when it is not the
// default, so the ordinary export omits it — and the default is the
// Authorization header:
//
//	postman-runtime 7.56.1 lib/authorizer/oauth2.js
//	params.addTokenTo = params.addTokenTo || HEADER; // Add token to header by default
//
// The importer defaulted to the query string instead. The result is a request
// that carries `?access_token=…` and no Authorization header, which fails at the
// server as an unauthenticated call — described in whatever vocabulary that
// server uses for an empty identity, and never in terms of the import. It is the
// worst kind of import bug: everything looks configured, the request is sent,
// and the error is about someone else's authorisation model.
//
// The other half is the vocabulary. Postman's value for the query placement is
// `queryParams`; matching only on "header" meant every unrecognised spelling —
// including the correct one — landed on the wrong branch.
package importers

import "testing"

func TestImportedOAuth2SendsTheTokenInTheHeaderByDefault(t *testing.T) {
	// An export with no addTokenTo at all: what Postman writes when the setting
	// is left alone, which is the overwhelmingly common case.
	auth := postmanOAuth2Auth(map[string]interface{}{
		"grant_type":     "client_credentials",
		"accessTokenUrl": "https://identity.example.test/token",
		"clientId":       "the-client",
		"clientSecret":   "s3cret",
	})
	if auth.TokenPlacement != "header" {
		t.Errorf("TokenPlacement = %q, want header — Postman's default", auth.TokenPlacement)
	}
}

func TestImportedOAuth2HonoursAnExplicitPlacement(t *testing.T) {
	for value, want := range map[string]string{
		"header": "header",
		// Postman's own spelling for the query placement.
		"queryParams": "url",
		// Accepted because LiteAPI stores it this way and a hand-edited or
		// round-tripped collection may carry either.
		"query": "url",
		"url":   "url",
		// Case is not guaranteed in a hand-edited export.
		"QUERYPARAMS": "url",
	} {
		auth := postmanOAuth2Auth(map[string]interface{}{
			"grant_type": "client_credentials",
			"addTokenTo": value,
		})
		if auth.TokenPlacement != want {
			t.Errorf("addTokenTo %q imported as %q, want %q", value, auth.TokenPlacement, want)
		}
	}
}

// An unknown value falls back to the default rather than to the query string.
// Guessing "query" for anything unrecognised is what produced the silent
// failure in the first place.
func TestImportedOAuth2FallsBackToTheHeaderForAnUnknownPlacement(t *testing.T) {
	auth := postmanOAuth2Auth(map[string]interface{}{
		"grant_type": "client_credentials",
		"addTokenTo": "somewhere-else",
	})
	if auth.TokenPlacement != "header" {
		t.Errorf("TokenPlacement = %q, want header", auth.TokenPlacement)
	}
}

// Client authentication is the OTHER placement in the same auth block, and its
// default runs the other way: absent means the Basic header. Pinned here so the
// two are not "fixed" into agreement.
func TestImportedOAuth2SendsClientCredentialsAsBasicByDefault(t *testing.T) {
	auth := postmanOAuth2Auth(map[string]interface{}{"grant_type": "client_credentials"})
	if auth.CredentialsPlacement != "basic_auth_header" {
		t.Errorf("CredentialsPlacement = %q", auth.CredentialsPlacement)
	}
	inBody := postmanOAuth2Auth(map[string]interface{}{
		"grant_type":            "client_credentials",
		"client_authentication": "body",
	})
	if inBody.CredentialsPlacement != "body" {
		t.Errorf("CredentialsPlacement = %q, want body", inBody.CredentialsPlacement)
	}
}
