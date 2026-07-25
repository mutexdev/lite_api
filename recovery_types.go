package main

import (
	"fmt"
	"time"
)

const recoveryEntryTTL = 7 * 24 * time.Hour

// RecoveryEntry is deliberately metadata-only. The private payload and state
// snapshots live outside state.json under the application's recovery folder.
type RecoveryEntry struct {
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
	State AppState      `json:"state"`
	Entry RecoveryEntry `json:"entry"`
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
