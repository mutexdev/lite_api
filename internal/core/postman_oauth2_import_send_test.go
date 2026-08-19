// An imported OAuth2 collection, sent.
//
// internal/importers pins the field; this pins the consequence, which is the
// part that was actually observed: a collection imported from Postman fetched a
// token correctly and then sent it somewhere the server does not look.
//
// The chain is short and every link was individually plausible. Postman omits
// `addTokenTo` when it is the default. The importer read that omission as "put
// it in the query string". applyOAuth2Token honoured that faithfully. So the
// request went out as `?access_token=…` with no Authorization header, the API
// resolved an empty identity, and answered in its own vocabulary — for the
// reporter, a SPIFFE ID with nothing inside the parentheses. Nothing in that
// message, or anywhere in the app, mentions the import.
//
// Two servers rather than one, because a single echo server cannot tell the
// difference between "the token was sent in the header" and "the token was sent
// at all".
package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestImportedOAuth2CollectionSendsABearerHeader(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"the-token","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	var (
		mu            sync.Mutex
		authorization string
		query         string
	)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorization = r.Header.Get("Authorization")
		query = r.URL.RawQuery
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer api.Close()

	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	workspace := state.Workspaces[0]

	// A collection as Postman exports it when "Add token to" was left alone:
	// the key is simply absent.
	collection := fmt.Sprintf(`{
		"info":{"name":"Imported OAuth2","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
		"auth":{"type":"oauth2","oauth2":[
			{"key":"grant_type","value":"client_credentials"},
			{"key":"accessTokenUrl","value":%q},
			{"key":"clientId","value":"the-client"},
			{"key":"clientSecret","value":"s3cret"}
		]},
		"item":[{"name":"Protected","request":{"method":"GET","url":{"raw":%q}}}]
	}`, tokenServer.URL, api.URL)

	state, err = app.ImportCollection(workspace.ID, ImportPayload{Kind: "postman", Content: collection})
	if err != nil {
		t.Fatalf("ImportCollection: %v", err)
	}
	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if imported.Auth.OAuth2.TokenPlacement != "header" {
		t.Fatalf("imported TokenPlacement = %q", imported.Auth.OAuth2.TokenPlacement)
	}
	if len(imported.Items) != 1 {
		t.Fatalf("imported %d items", len(imported.Items))
	}

	if _, err := app.SendRequest(imported.ID, imported.Items[0].ID, ""); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if authorization != "Bearer the-token" {
		t.Errorf("Authorization on the wire was %q, want the bearer token", authorization)
	}
	// The other half of the assertion. A token in BOTH places would pass the
	// check above while still leaking the credential into a URL that servers,
	// proxies and access logs all record.
	if strings.Contains(query, "access_token") {
		t.Errorf("the token was also put in the query string: %q", query)
	}
}
