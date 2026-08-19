package core

import (
	"strings"
	"testing"
)

func TestNativeClosePromptMessageNamesAndBoundsDrafts(t *testing.T) {
	drafts := []UnsavedDraft{
		{Name: "One"},
		{Name: "Two"},
		{Name: "Three"},
		{Name: "Four"},
		{Name: "Five"},
		{Name: "Six"},
	}
	message := nativeClosePromptMessage(drafts)
	for _, expected := range []string{"6 unsaved requests", "\u2022 One", "\u2022 Five", "and 1 more", "cancel quitting"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("close message missing %q:\n%s", expected, message)
		}
	}
	if strings.Contains(message, "\u2022 Six") {
		t.Fatalf("close message should bound the visible request list:\n%s", message)
	}
}

func TestNativeClosePromptMessageUsesSafeUntitledFallback(t *testing.T) {
	message := nativeClosePromptMessage([]UnsavedDraft{{Name: "  "}})
	if !strings.Contains(message, "1 unsaved request will") || !strings.Contains(message, "Untitled request") {
		t.Fatalf("unexpected close message:\n%s", message)
	}
}
