// Environments and .env files.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

import "time"

type Environment struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Color     string     `json:"color"`
	Variables []Variable `json:"variables"`
}

type DotEnvFile struct {
	Scope     string    `json:"scope"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	Runtime   bool      `json:"runtime"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}
