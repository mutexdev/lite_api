package core

import (
	"context"
	"sync"
	"testing"
)

// beforeClose is the guard on the macOS close control, Cmd+Q and the
// application menu, and it was at 0%. Its middle is a Wails dialog and cannot
// run here — but three of its decisions are reached BEFORE that dialog, and
// those are the ones that decide whether the app can be closed at all.
//
// The convention is inverted from what the name suggests and worth stating:
// the return value is `prevent`, so TRUE keeps the window open and FALSE lets
// it close. A guard that returned the wrong one either traps the user in an
// app they cannot quit, or quits past their unsaved work.
//
// Every case below is checked with a plain context: reaching the dialog would
// panic or block, so a test that passes is also evidence the dialog was never
// reached.

func TestBeforeCloseAllowsTheCloseWhenNothingIsUnsaved(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}
	drafts, err := app.ListUnsavedDrafts()
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("this test needs a clean app; it has %d unsaved drafts", len(drafts))
	}

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Error("the close was prevented with nothing unsaved, so the app could not be quit")
	}
}

// A nil receiver prevents the close rather than panicking. Wails holds this
// callback for the lifetime of the process, and a panic in it would take the
// app down during the user's own quit.
func TestBeforeCloseOnANilAppPreventsRatherThanPanics(t *testing.T) {
	var app *App
	if prevent := app.beforeClose(context.Background()); !prevent {
		t.Error("a nil app allowed the close")
	}
}

// THE REENTRANCY GUARD. Cmd+Q while the unsaved-changes dialog is already open
// is an ordinary thing to do, and macOS will happily deliver the second event.
// The second call must decline immediately instead of opening a second dialog
// over the first — two modal dialogs contending for the same drafts is how one
// of them acts on state the other has already changed.
func TestBeforeCloseDeclinesWhileAPromptIsAlreadyOpen(t *testing.T) {
	app := newAppForTest(t)

	if !nativeClosePromptActive.CompareAndSwap(false, true) {
		t.Fatal("the prompt flag was already set before this test began")
	}
	defer nativeClosePromptActive.Store(false)

	if prevent := app.beforeClose(context.Background()); !prevent {
		t.Error("a second close was allowed while a prompt was already open")
	}
}

// And the flag is RELEASED on the way out, or the first quit would be the last
// one the app ever accepts.
func TestBeforeCloseReleasesThePromptFlag(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("the first close was prevented, so this test cannot say anything about the flag")
	}
	if nativeClosePromptActive.Load() {
		t.Fatal("the prompt flag was left set, so every later quit would be declined")
	}
	// The second attempt proves it rather than only inspecting the flag.
	if prevent := app.beforeClose(context.Background()); prevent {
		t.Error("a second close was declined, so the flag was not released")
	}
}

// Concurrent close events must not both proceed. Exactly one may pass the
// guard; the rest decline. Under -race this also says the flag is the real
// synchronisation rather than incidental timing.
func TestBeforeCloseAdmitsOnlyOneCallerAtATime(t *testing.T) {
	app := newAppForTest(t)
	if _, err := app.GetState(); err != nil {
		t.Fatal(err)
	}

	// Hold the guard so every concurrent caller must decline.
	if !nativeClosePromptActive.CompareAndSwap(false, true) {
		t.Fatal("the prompt flag was already set before this test began")
	}
	defer nativeClosePromptActive.Store(false)

	var wg sync.WaitGroup
	results := make([]bool, 8)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = app.beforeClose(context.Background())
		}(i)
	}
	wg.Wait()

	for index, prevent := range results {
		if !prevent {
			t.Errorf("caller %d was admitted while a prompt was already open", index)
		}
	}
	// The guard the test itself holds must survive every declined attempt: a
	// decline that cleared it would let the NEXT event open a second dialog.
	if !nativeClosePromptActive.Load() {
		t.Error("a declined call cleared the flag held by the open prompt")
	}
}
