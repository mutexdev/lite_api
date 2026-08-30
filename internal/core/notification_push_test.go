package core

// Notifications the user did not trigger have to be pushed.
//
// notify() only ever mutated a.state, so a notification reached the frontend
// when — and only when — some binding call happened to return an AppState
// afterwards. That is fine for "Saved X", which is the response to the user's
// own click. It is not fine for the collection watcher, the background persist
// writer or a decrypt failure during hydration: those raise an error with no
// call in flight to carry it, and it sat unseen until the user opened the
// notification list for unrelated reasons.

import (
	"strings"
	"testing"
)

func recordPushedNotifications(app *App) *[]Notification {
	pushed := &[]Notification{}
	app.notificationEmit = func(notification Notification) {
		*pushed = append(*pushed, notification)
	}
	return pushed
}

func TestErrorAndWarningNotificationsArePushed(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}
	pushed := recordPushedNotifications(app)

	app.mu.Lock()
	app.notify("error", "disk is on fire")
	app.notify("warning", "partially imported")
	app.mu.Unlock()

	if len(*pushed) != 2 {
		t.Fatalf("expected the error and the warning to be pushed, got %#v", *pushed)
	}
	if !strings.Contains((*pushed)[0].Message, "disk is on fire") || (*pushed)[0].Level != "error" {
		t.Fatalf("the error notification was pushed wrong: %#v", (*pushed)[0])
	}
	if (*pushed)[1].Level != "warning" {
		t.Fatalf("the warning notification was pushed wrong: %#v", (*pushed)[1])
	}
}

func TestSuccessAndInfoNotificationsAreNotPushed(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}
	pushed := recordPushedNotifications(app)

	app.mu.Lock()
	app.notify("success", "Saved Ping")
	app.notify("info", "nothing to report")
	app.mu.Unlock()

	if len(*pushed) != 0 {
		t.Fatalf("success and info ride back on the AppState the binding returns and must stay pull-only, got %#v", *pushed)
	}
}

// A permanently unwritable data directory has to keep saying so. Reporting only
// the first failure of a streak meant the one notification about it could be
// hours old and already marked read while every edit since was being lost.
func TestPersistFailureIsReportedAgainDuringALongStreak(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}

	reportedAt := []int{}
	for streak := 0; streak < 3*persistFailureReportInterval; streak++ {
		app.mu.Lock()
		before := len(app.state.Notifications)
		app.persistMu.Lock()
		app.persistFailures = streak
		app.persistMu.Unlock()
		app.reportPersistFailureLocked(errPersistFailureForTest)
		if len(app.state.Notifications) > before {
			reportedAt = append(reportedAt, streak)
		}
		app.mu.Unlock()
	}

	want := []int{0, persistFailureReportInterval, 2 * persistFailureReportInterval}
	if len(reportedAt) != len(want) {
		t.Fatalf("a streak of %d failures reported %v; want one report at each of %v", 3*persistFailureReportInterval, reportedAt, want)
	}
	for index, streak := range want {
		if reportedAt[index] != streak {
			t.Fatalf("reports landed at %v, want %v", reportedAt, want)
		}
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if !hasNotificationContaining(app.state.Notifications, "error", "consecutive failures") {
		t.Fatalf("a repeat report must say it is a repeat: %#v", app.state.Notifications)
	}
}

var errPersistFailureForTest = &persistFailureForTest{}

type persistFailureForTest struct{}

func (*persistFailureForTest) Error() string { return "data directory is unwritable" }

// A secret that cannot be decrypted has to become visible somewhere. Hydration
// deliberately does not fail — every readable secret still loads — so the
// error-level notification is the only place it is said at all.
func TestUnreadableCollectionSecretsRaiseAnErrorNotification(t *testing.T) {
	t.Setenv("LITEAPI_SECRET_KEY", "core-hydrate-test-key")
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[0]
	if collection.Path == "" {
		t.Skip("default collection is not filesystem-backed in this fixture")
	}

	if _, err := app.CreateEnvironment(collection.ID, "Production"); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	// Replace the stored ciphertext with something no key can read, which is
	// what a rotated machine key or a copied data directory produces.
	app.mu.Lock()
	store, err := app.readEnvironmentSecretsLocked()
	if err != nil {
		app.mu.Unlock()
		t.Fatalf("readEnvironmentSecretsLocked: %v", err)
	}
	corrupted := false
	for ci := range store.Collections {
		for ei := range store.Collections[ci].Environments {
			for vi := range store.Collections[ci].Environments[ei].Secrets {
				store.Collections[ci].Environments[ei].Secrets[vi].Value = "$01:not-hexadecimal"
				corrupted = true
			}
		}
	}
	if !corrupted {
		app.mu.Unlock()
		t.Fatal("the fixture environment stored no secrets to corrupt")
	}
	if err := app.writeEnvironmentSecretsLocked(store); err != nil {
		app.mu.Unlock()
		t.Fatalf("writeEnvironmentSecretsLocked: %v", err)
	}
	live, err := app.findCollectionLocked(collection.ID)
	if err != nil {
		app.mu.Unlock()
		t.Fatalf("findCollectionLocked: %v", err)
	}
	err = app.hydrateCollectionEnvironmentSecretsLocked(live)
	notifications := append([]Notification(nil), app.state.Notifications...)
	app.mu.Unlock()
	if err != nil {
		t.Fatalf("hydration must still complete for the readable secrets: %v", err)
	}
	if !hasNotificationContaining(notifications, "error", "could not decrypt") {
		t.Fatalf("an unreadable secret arrived as a blank value with nothing said about it: %#v", notifications)
	}
}
