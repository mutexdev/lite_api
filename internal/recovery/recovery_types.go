package recovery

import (
	"fmt"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

// The kinds an Entry can record. They describe what was deleted, so they belong
// with the entry rather than with the caller that happens to stage one.
const (
	KindRequest    = "request"
	KindFolder     = "folder"
	KindCollection = "collection"
)

const recoveryEntryTTL = 7 * 24 * time.Hour

// Entry is deliberately metadata-only. The private payload and state
// snapshots live outside state.json under the application's recovery folder.
type Entry struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	DisplayName  string    `json:"displayName"`
	WorkspaceID  string    `json:"workspaceId"`
	CollectionID string    `json:"collectionId"`
	DeletedAt    time.Time `json:"deletedAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Restorable   bool      `json:"restorable"`
}

// RecoverableDeleteResult gives callers the normal state update and a durable
// recovery handle suitable for an explicit Restore action.
type RecoverableDeleteResult struct {
	State types.AppState `json:"state"`
	Entry Entry          `json:"entry"`
}

// RestoreConflictError means recovery data was retained, but applying it
// would overwrite files or collection state changed after the delete.
type RestoreConflictError struct {
	EntryID string
	Reason  string
}

func (e *RestoreConflictError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("recovery entry %s cannot be restored safely", e.EntryID)
	}
	return fmt.Sprintf("recovery entry %s cannot be restored safely: %s", e.EntryID, e.Reason)
}
