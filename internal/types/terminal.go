// A terminal session as the frontend sees it; the live process is unexported and stays in package main.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in package main are a Go shim and not a Wails one.
package types

type TerminalSession struct {
	ID        string `json:"id"`
	CWD       string `json:"cwd"`
	PID       int    `json:"pid"`
	Output    string `json:"output"`
	Exited    bool   `json:"exited"`
	ExitCode  int    `json:"exitCode"`
	Signal    string `json:"signal"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
