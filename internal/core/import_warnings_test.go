package core

// An import that dropped something has to say so.
//
// The Postman importer skips an item it cannot decode and names it rather than
// failing the whole file — a hundred-request collection is not thrown away over
// one malformed entry. ImportCollection called the wrapper that discards that
// list, so the drop was announced as a plain success and the missing request
// was discovered later, or never.

import (
	"strings"
	"testing"
)

const postmanCollectionWithAnUnreadableItem = `{
  "info": {"name": "Partly Readable", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [
    {"name": "Good", "request": {"method": "GET", "url": {"raw": "https://example.test/good"}}},
    "this entry is a string, not an item",
    {"name": "Also Good", "request": {"method": "GET", "url": {"raw": "https://example.test/also-good"}}}
  ]
}`

func TestImportCollectionSurfacesPostmanImportWarnings(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}

	state, err = app.ImportCollection(state.Workspaces[0].ID, ImportPayload{
		Kind:    "postman",
		Name:    "Partly Readable",
		Content: postmanCollectionWithAnUnreadableItem,
	})
	if err != nil {
		t.Fatalf("ImportCollection: %v", err)
	}

	imported := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	if len(imported.Items) != 2 {
		t.Fatalf("expected the two readable requests to import, got %d", len(imported.Items))
	}
	// The warning names the entry that was dropped. This one has no name of its
	// own -- it is a bare string where an object belongs -- so it is named by
	// its position, which is what someone opening the file has to go on.
	if !hasNotificationContaining(state.Notifications, "warning", `skipped "entry 2"`) {
		t.Fatalf("the import dropped an item and reported nothing but success: %#v", state.Notifications)
	}
	if !hasNotificationContaining(state.Notifications, "success", "Imported Partly Readable") {
		t.Fatalf("the success notification is still owed for a partial import: %#v", state.Notifications)
	}
}

func TestImportCollectionRaisesNoWarningForACleanImport(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	clean := strings.Replace(postmanCollectionWithAnUnreadableItem, `    "this entry is a string, not an item",
`, "", 1)

	state, err = app.ImportCollection(state.Workspaces[0].ID, ImportPayload{
		Kind:    "postman",
		Name:    "Fully Readable",
		Content: clean,
	})
	if err != nil {
		t.Fatalf("ImportCollection: %v", err)
	}
	for _, notification := range state.Notifications {
		if notification.Level == "warning" && strings.Contains(notification.Message, "with warnings") {
			t.Fatalf("a clean import raised a warning: %#v", notification)
		}
	}
}
