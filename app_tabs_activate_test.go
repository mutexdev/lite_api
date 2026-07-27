package main

import (
	"testing"
)

// SetActiveTab was at 0%. It looks trivial, and the one thing in it that is not
// trivial is the distinction its helper's comment spells out and which no test
// held: found=false means there is no such tab, while found=true with a non-nil
// error means the tab DID switch and a background write failed.
//
// Those two must not be collapsed. Returning an empty AppState on a parked
// write failure would blank the user's whole window because a persist retry is
// pending, and swallowing the not-found error would leave the UI showing a tab
// the backend does not think is active.

func openTabsFor(t *testing.T, app *App) (string, []string) {
	t.Helper()
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	// The requests are CREATED here rather than taken from whatever the default
	// collection happens to ship with. Depending on its contents made an earlier
	// version of this fixture skip on every run — a test that reports success
	// while checking nothing, which is the failure qa/skip-audit.sh exists for.
	ids := make([]string, 0, 2)
	for _, name := range []string{"Tab one", "Tab two"} {
		created, err := app.CreateRequest(collectionID, "http", name)
		if err != nil {
			t.Fatal(err)
		}
		item := findRequestByNameForTest(t, created, collectionID, name)
		state, err = app.OpenRequestTab(collectionID, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, collectionID+":"+item.ID)
	}
	return state.ActiveTabID, ids
}

func TestSetActiveTabSwitchesToAnOpenTab(t *testing.T) {
	app := newAppForTest(t)
	active, tabs := openTabsFor(t, app)
	if active == tabs[0] {
		t.Fatalf("precondition: opening the second tab should have made it active, got %q", active)
	}

	state, err := app.SetActiveTab(tabs[0])
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTabID != tabs[0] {
		t.Errorf("returned state has active tab %q, want %q", state.ActiveTabID, tabs[0])
	}
	// And it is not merely the returned copy that changed.
	fresh, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ActiveTabID != tabs[0] {
		t.Errorf("stored active tab is %q, want %q", fresh.ActiveTabID, tabs[0])
	}
}

// A tab id the backend does not hold is refused, and the selection is left
// where it was rather than cleared. Clearing it would leave the window with no
// active tab because the frontend asked for one that had already closed.
func TestSetActiveTabRefusesATabItDoesNotHold(t *testing.T) {
	app := newAppForTest(t)
	_, tabs := openTabsFor(t, app)
	if _, err := app.SetActiveTab(tabs[0]); err != nil {
		t.Fatal(err)
	}

	for _, missing := range []string{"", "no-such-tab", "col:item"} {
		state, err := app.SetActiveTab(missing)
		if err == nil {
			t.Errorf("switching to %q was accepted", missing)
		}
		// The RETURNED STATE is the other half of the contract, and checking
		// only the error missed it: not-found returns the zero AppState, while
		// a parked background-write failure returns the live one alongside its
		// error. Collapsing the two is a mutation the error check alone could
		// not see.
		if len(state.Workspaces) != 0 || state.ActiveTabID != "" {
			t.Errorf("a refused switch to %q returned a populated state (%d workspaces, active %q)",
				missing, len(state.Workspaces), state.ActiveTabID)
		}
		fresh, err := app.GetState()
		if err != nil {
			t.Fatal(err)
		}
		if fresh.ActiveTabID != tabs[0] {
			t.Errorf("a refused switch to %q moved the active tab to %q", missing, fresh.ActiveTabID)
		}
	}
}

func TestSetActiveTabIsIdempotent(t *testing.T) {
	app := newAppForTest(t)
	_, tabs := openTabsFor(t, app)

	for i := 0; i < 3; i++ {
		state, err := app.SetActiveTab(tabs[1])
		if err != nil {
			t.Fatal(err)
		}
		if state.ActiveTabID != tabs[1] {
			t.Fatalf("call %d left the active tab at %q", i+1, state.ActiveTabID)
		}
	}
}

// firstNonZero picks the first value that was actually set, and zero is a
// meaningful "unset" for the int settings it serves. It must not stop at a
// zero, and it must not skip a negative one — a negative is a set value, even
// if it is an invalid one for the caller to reject later.
func TestFirstNonZeroSkipsOnlyZeroes(t *testing.T) {
	cases := []struct {
		name   string
		values []int
		want   int
	}{
		{"nothing at all", nil, 0},
		{"all zero", []int{0, 0, 0}, 0},
		{"first wins", []int{7, 9}, 7},
		{"skips leading zeroes", []int{0, 0, 9}, 9},
		{"a negative counts as set", []int{0, -1, 9}, -1},
		{"a single value", []int{5}, 5},
		{"trailing zero is ignored", []int{5, 0}, 5},
	}
	for _, testCase := range cases {
		if got := firstNonZero(testCase.values...); got != testCase.want {
			t.Errorf("%s: firstNonZero(%v) = %d, want %d", testCase.name, testCase.values, got, testCase.want)
		}
	}
}
