package core

// The Postman export gaps that only show up from the application side: a
// partial import producing requests whose folder no longer exists, the auth
// matrix across all three levels, and what the export summary claims.

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/export"
	"github.com/mutexdev/lite_api/internal/importers"
	"github.com/mutexdev/lite_api/internal/types"
)

// THE PARTIAL-IMPORT REPRO. Requests and folders are selected through two
// independent lists, so a user can keep a request and drop the folder it lives
// in. The result used to be a collection whose export contained the request
// nowhere at all.
func TestPartialImportKeepsTheFolderChainOfAKeptRequest(t *testing.T) {
	nested := types.NewRequestItem("Nested", "http", 1)
	nested.FolderPath = "Reports/Weekly"
	root := types.NewRequestItem("Root", "http", 2)
	collection := Collection{
		Name: "Partial",
		Folders: []FolderConfig{
			{Path: "Reports", DisplayPath: "Reports", Name: "Reports", Seq: 1},
			{Path: "Reports/Weekly", DisplayPath: "Reports/Weekly", Name: "Weekly", Seq: 2},
		},
		Items: []RequestItem{nested, root},
	}
	selection := CollectionImportSelection{
		SourceID:       "source-1",
		FilterRequests: true,
		FilterFolders:  true,
		RequestIDs: []string{
			collectionImportRequestSelectionID("source-1", nested, 0),
			collectionImportRequestSelectionID("source-1", root, 1),
		},
		// Every folder deselected, while a request inside one is kept.
		FolderIDs: nil,
	}

	filtered, warnings := filterImportedCollection(collection, selection)

	if len(filtered.Folders) != 2 {
		t.Fatalf("got %d folders, want the chain of the kept request re-added: %+v", len(filtered.Folders), filtered.Folders)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want the user told the selection was widened", warnings)
	}

	result, err := export.BuildPostmanExport(filtered)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestCount != 2 {
		t.Errorf("RequestCount = %d, want 2", result.RequestCount)
	}
	if !strings.Contains(result.Content, "Nested") {
		t.Errorf("the kept request is in no exported folder:\n%s", result.Content)
	}
}

// And even when nothing repairs the collection — a hand-edited state, an older
// import — the export must still emit the request rather than lose it.
func TestPostmanExportEmitsRequestsWithNoFolderConfigAtAll(t *testing.T) {
	orphan := types.NewRequestItem("Orphan", "http", 1)
	orphan.FolderPath = "Gone/Deeper"

	result, err := export.BuildPostmanExport(Collection{Name: "Orphan", Items: []RequestItem{orphan}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestCount != 1 || !strings.Contains(result.Content, "Orphan") {
		t.Errorf("RequestCount = %d and the request %s in the file", result.RequestCount, map[bool]string{true: "is", false: "is not"}[strings.Contains(result.Content, "Orphan")])
	}
}

// Every auth mode, at all three levels, through a full round trip. A mode that
// exports as nothing comes back as inherit, which is a different request.
func TestPostmanExportRoundTripsEveryAuthModeAtEveryLevel(t *testing.T) {
	modes := []AuthConfig{
		{Mode: "none", APILocation: "header"},
		{Mode: "basic", Username: "u", Password: "p", APILocation: "header"},
		{Mode: "bearer", Token: "t", APILocation: "header"},
		{Mode: "apikey", APIKey: "k", APIValue: "v", APILocation: "query"},
		{Mode: "digest", Username: "du", Password: "dp", APILocation: "header"},
		{Mode: "awsv4", APILocation: "header", AWSV4: types.AWSV4Auth{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST", Service: "execute-api", Region: "eu-west-1"}},
		{Mode: "oauth1", APILocation: "header", OAuth1: types.OAuth1Auth{ConsumerKey: "ck", ConsumerSecret: "cs", AccessToken: "at", AccessTokenSecret: "ats", SignatureMethod: "HMAC-SHA1", PrivateKeyType: "text", Version: "1.0", Placement: "header"}},
		{Mode: "oauth2", APILocation: "header", OAuth2: types.OAuth2Auth{GrantType: "authorization_code", PKCE: true, AuthorizationURL: "https://auth.test/a", AccessTokenURL: "https://auth.test/t", CallbackURL: "https://app.test/cb", ClientID: "id", ClientSecret: "secret", Scope: "read", TokenPlacement: "header", TokenQueryKey: "access_token", CredentialsPlacement: "basic_auth_header"}},
	}

	for _, auth := range modes {
		t.Run(auth.Mode, func(t *testing.T) {
			item := types.NewRequestItem("Request", "http", 1)
			item.URL = "https://example.test/thing"
			item.FolderPath = "Folder"
			item.Auth = auth
			source := Collection{
				Name:    "Auth",
				Auth:    auth,
				Folders: []FolderConfig{{Path: "Folder", DisplayPath: "Folder", Name: "Folder", Seq: 1, Auth: auth}},
				Items:   []RequestItem{item},
			}

			exported, _, _, err := export.BuildPostmanCollection(source)
			if err != nil {
				t.Fatal(err)
			}
			imported, err := importers.ImportPostman(exported, "auth", false)
			if err != nil {
				t.Fatal(err)
			}

			if imported.Auth.Mode != auth.Mode {
				t.Errorf("collection auth = %q, want %q", imported.Auth.Mode, auth.Mode)
			}
			if len(imported.Folders) != 1 || imported.Folders[0].Auth.Mode != auth.Mode {
				t.Errorf("folder auth = %+v, want %q", imported.Folders, auth.Mode)
			}
			if len(imported.Items) != 1 || imported.Items[0].Auth.Mode != auth.Mode {
				t.Errorf("request auth = %+v, want %q", imported.Items, auth.Mode)
			}

			// And the credentials themselves, not merely the mode. A second
			// cycle pins that what came back is what goes out again.
			second, err := export.BuildPostmanExport(imported)
			if err != nil {
				t.Fatal(err)
			}
			twice, err := importers.ImportPostman(second.Content, "auth", false)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := fidelityAuth(twice.Items[0].Auth), fidelityAuth(imported.Items[0].Auth); got != want {
				t.Errorf("the credentials drift on a second cycle\n got %s\nwant %s", got, want)
			}
			if auth.Mode == "awsv4" && imported.Items[0].Auth.AWSV4.SecretAccessKey != "SK" {
				t.Errorf("awsv4 secret lost: %+v", imported.Items[0].Auth.AWSV4)
			}
			if auth.Mode == "oauth2" && (!imported.Items[0].Auth.OAuth2.PKCE || imported.Items[0].Auth.OAuth2.ClientSecret != "secret") {
				t.Errorf("oauth2 config lost: %+v", imported.Items[0].Auth.OAuth2)
			}
		})
	}
}

// A Postman collection carries no environments, and the summary said it did.
func TestPostmanExportSummaryCountsWhatTheFileHolds(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	orphan := types.NewRequestItem("Orphan", "http", 1)
	orphan.FolderPath = "Ghost"
	app.mu.Lock()
	collection := &app.state.Workspaces[0].Collections[0]
	collection.Folders = nil
	collection.Items = []RequestItem{orphan}
	collection.Environments = []Environment{{ID: "env-prod", Name: "Production"}}
	app.mu.Unlock()

	result, err := app.ExportCollectionWithOptions(collectionID, CollectionExportOptions{Format: "postman"})
	if err != nil {
		t.Fatal(err)
	}
	if result.EnvironmentCount != 0 {
		t.Errorf("EnvironmentCount = %d; a Postman export carries no environments, so this told the user something was exported that is not in the file", result.EnvironmentCount)
	}
	if result.RequestCount != 1 || result.FolderCount != 1 {
		t.Errorf("summary = %d requests in %d folders, want 1 in 1 (the folder exists only as the request's FolderPath)", result.RequestCount, result.FolderCount)
	}
	if !strings.Contains(result.Content, "Orphan") {
		t.Errorf("the request is missing from the export:\n%s", result.Content)
	}
}
