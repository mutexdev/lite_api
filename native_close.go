package main

import (
	"LiteAPI/internal/cookiejar"
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	nativeCloseSaveButton    = "Save and Quit"
	nativeCloseDiscardButton = "Discard Changes"
	nativeCloseCancelButton  = "Cancel"
)

var nativeClosePromptActive atomic.Bool

// beforeClose is the platform-level guard for the macOS window close control,
// Cmd+Q, and the application menu. Tab-scoped close commands remain in the
// Svelte workbench so they can preserve focus and tab context.
func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	if a == nil || !nativeClosePromptActive.CompareAndSwap(false, true) {
		return true
	}
	defer nativeClosePromptActive.Store(false)

	drafts, err := a.ListUnsavedDrafts()
	if err != nil {
		showNativeCloseError(ctx, err)
		return true
	}
	if len(drafts) == 0 {
		return false
	}

	choice, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.WarningDialog,
		Title:         "Unsaved Changes",
		Message:       nativeClosePromptMessage(drafts),
		Buttons:       []string{nativeCloseSaveButton, nativeCloseDiscardButton, nativeCloseCancelButton},
		DefaultButton: nativeCloseSaveButton,
		CancelButton:  nativeCloseCancelButton,
	})
	if err != nil {
		showNativeCloseError(ctx, err)
		return true
	}

	switch choice {
	case nativeCloseSaveButton:
		if _, err := a.SaveUnsavedDrafts(drafts); err != nil {
			showNativeCloseError(ctx, fmt.Errorf("save changes: %w\n\nSome earlier requests may already be saved. LiteAPI remains open so you can review the remaining unsaved requests", err))
			return true
		}
		return false
	case nativeCloseDiscardButton:
		if _, err := a.DiscardUnsavedDrafts(drafts); err != nil {
			showNativeCloseError(ctx, fmt.Errorf("discard changes: %w\n\nSome earlier requests may already be resolved. LiteAPI remains open so you can review the remaining unsaved requests", err))
			return true
		}
		return false
	default:
		return true
	}
}

func nativeClosePromptMessage(drafts []UnsavedDraft) string {
	if len(drafts) == 0 {
		return "Quit LiteAPI?"
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%d unsaved request%s will be affected:\n", len(drafts), cookiejar.PluralSuffix(len(drafts)))
	const visibleLimit = 5
	for i, draft := range drafts {
		if i == visibleLimit {
			fmt.Fprintf(&message, "\u2022 and %d more\n", len(drafts)-visibleLimit)
			break
		}
		name := strings.TrimSpace(draft.Name)
		if name == "" {
			name = "Untitled request"
		}
		fmt.Fprintf(&message, "\u2022 %s\n", name)
	}
	message.WriteString("\nSave the requests, discard their unsaved changes, or cancel quitting.")
	return message.String()
}

func showNativeCloseError(ctx context.Context, err error) {
	_, _ = wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.ErrorDialog,
		Title:         "LiteAPI Could Not Quit",
		Message:       err.Error(),
		Buttons:       []string{"OK"},
		DefaultButton: "OK",
	})
}
