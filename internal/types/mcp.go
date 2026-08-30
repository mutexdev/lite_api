// What the Settings panel shows for the MCP agent interface.
//
// See docs/mcp-agent-interface.md; the preference half of this lives on
// Preferences.MCP in preferences.go.
package types

import "time"

// MCPStatus is the pairing view: whether the server is meant to run, whether it
// actually is, and the one command that connects an agent to it.
//
// TOKEN IS PRESENT HERE ON PURPOSE, and it is the one place it appears. This
// struct crosses the Wails boundary into the app's own Settings window, which
// is how the user learns the credential they are about to paste into their
// agent's configuration — the pairing flow has to show it once or there is no
// way to pair at all. It is never returned over MCP itself: nothing in
// mcpserver.Backend has a field that could carry it.
type MCPStatus struct {
	// Enabled is the user's preference; Running is the observed listener. They
	// disagree while a bind is failing, which is exactly when the user needs to
	// see both rather than one derived "on/off".
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Port is the resolved port when running, and the configured one otherwise,
	// so the command below always names something.
	Port      int    `json:"port"`
	Token     string `json:"token"`
	Command   string `json:"command"`
	LastError string `json:"lastError,omitempty"`
}

// MCPAuditEntry is one recorded MCP tool call, as the audit panel reads it back.
//
// Rule 6 of docs/mcp-agent-interface.md: from Phase 2 every call an agent makes
// is recorded and visible in the app. The entry is deliberately thin — what was
// called, a compact rendering of the arguments, how it ended, how long it took.
// It carries no result payload, because a run's response body can be megabytes
// and the audit log is a record of ACTIVITY, not a second copy of the data.
//
// ArgsSummary is produced by internal/mcpserver, which builds it from arguments
// that have already passed the redaction rules, and is persisted verbatim.
type MCPAuditEntry struct {
	At          time.Time `json:"at"`
	Tool        string    `json:"tool"`
	ArgsSummary string    `json:"argsSummary,omitempty"`
	// Outcome is "ok", "error" or "denied". A denial is kept distinct from an
	// error on purpose: "the guard stopped this" and "this broke" are different
	// events for the user reading the panel.
	Outcome    string `json:"outcome"`
	DurationMs int    `json:"durationMs"`
}

// MCPApprovalRequest is the payload of the "mcp:approval" event — the prompt the
// new-host guard (rule 4) raises when a run would resolve a secret into a
// request aimed at a host the collection has never sent that secret to.
//
// SECRET NAMES, NEVER VALUES. The whole point of the prompt is to tell the user
// which credential is about to travel somewhere new; naming it is what makes the
// decision informed, and the value would defeat the boundary the guard exists to
// hold. The frontend answers with App.ResolveMCPApproval(id, approve, remember).
type MCPApprovalRequest struct {
	ID          string   `json:"id"`
	RequestName string   `json:"requestName"`
	Host        string   `json:"host"`
	SecretNames []string `json:"secretNames"`
}

// MCPApproval is one remembered (secret, host) pair from an approval the user
// chose to keep. Persisted to <dataDir>/mcp-approvals.json, and unioned into the
// host allowlist the guard computes from the collections themselves.
//
// Host is stored lowercased and without a port, matching how the guard resolves
// a run's target. ApprovedAt is RFC3339 and is there for the user's benefit —
// nothing keys off it.
type MCPApproval struct {
	Secret     string `json:"secret"`
	Host       string `json:"host"`
	ApprovedAt string `json:"approvedAt,omitempty"`
}

// MCPApprovalFile is the on-disk shape of the remembered approvals. A wrapper
// object rather than a bare array so a later field (a version, an expiry) can be
// added without rewriting every installed file.
type MCPApprovalFile struct {
	Approvals []MCPApproval `json:"approvals"`
}
