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

// MCPApprovalRequest is the payload of the "mcp:approval" event — the prompt
// raised when an agent-initiated run would contact an origin that nothing in the
// request's own definition points at, under the environment the run is using.
//
// IT NAMES A SITE, NOT JUST A HOST. The question the user is answering is not
// "may this credential reach example.com" but "may THIS request, under THIS
// environment, contact THIS origin" — so the payload carries the whole site
// (workspace, collection, request, selected collection environment, active
// global environments) plus the egress kind, and the dialog spells all of it
// out. A prompt that named only the host would ask a question broader than the
// approval it produces, which is how a user ends up granting something they were
// never shown.
//
// SECRET NAMES, NEVER VALUES, AND ONLY AS ADVICE. SecretNames says which
// credentials the request references, because that is what makes the decision
// feel real to the person answering. It is advisory text only: nothing keys an
// approval on it, and no enforcement anywhere consults it.
//
// The frontend answers with App.ResolveMCPApproval(id, approve, remember).
type MCPApprovalRequest struct {
	ID string `json:"id"`
	// RunLabel is what the run calls itself in the first line of the prompt —
	// the request's name for a single run, the flow's for a flow step.
	RunLabel string `json:"runLabel,omitempty"`

	// --- the site (see MCPApproval; these are the fields the key is built from)
	WorkspacePath          string   `json:"workspacePath,omitempty"`
	CollectionID           string   `json:"collectionId,omitempty"`
	CollectionName         string   `json:"collectionName,omitempty"`
	RequestID              string   `json:"requestId,omitempty"`
	RequestName            string   `json:"requestName"`
	EnvironmentID          string   `json:"environmentId,omitempty"`
	EnvironmentName        string   `json:"environmentName,omitempty"`
	GlobalEnvironmentIDs   []string `json:"globalEnvironmentIds,omitempty"`
	GlobalEnvironmentNames []string `json:"globalEnvironmentNames,omitempty"`

	// Origin is the canonical scheme://host:port the run would contact — the
	// exact text the approval is remembered under.
	Origin string `json:"origin,omitempty"`
	// Kind is the egress kind (main, redirect, script, token, aws) and KindClass
	// the class its approvals are keyed by (request, token, aws). Both are shown
	// so the user can tell "this request's own destination" from "the endpoint
	// that mints its token".
	Kind      string `json:"kind,omitempty"`
	KindClass string `json:"kindClass,omitempty"`

	// Host is the bare hostname the SHIPPED host guard (mcp_guard.go) reasons
	// about. It is kept alongside Origin only while that guard is still the
	// enforcing boundary; the destination boundary keys on Origin, and the final
	// wave that retires the old guard retires this field with it.
	Host string `json:"host,omitempty"`
	// SecretNames is advisory text — the credentials the request references.
	SecretNames []string `json:"secretNames,omitempty"`
}

// MCPApproval is one remembered "this request, under this environment, may
// contact this origin" decision. Persisted to <dataDir>/mcp-approvals.json.
//
// EVERY FIELD IS PART OF THE KEY, AND EVERY ONE OF THEM NARROWS. An approval
// remembered for request A never authorizes request B; one remembered under the
// dev environment never authorizes the same request under production, because
// production resolves the request's variables to somewhere else and the user's
// "yes" was about the destination they were shown. GlobalEnvironmentIDs is an
// ordered LIST even though at most one global environment is active today, so a
// future multi-active model cannot silently widen an approval made under the
// single-active one.
//
// KindClass ("request", "token" or "aws") keeps an approval for a request's own
// destination from authorizing its OAuth token endpoint, and vice versa.
//
// NO OMITEMPTY ON THE KEY FIELDS, deliberately. An empty EnvironmentID means "no
// collection environment selected", which is a real and common configuration;
// omitting it from the file would make the entry indistinguishable on reload
// from one written before these fields existed, and the migration rule
// (mcp_approvals.go) ignores an entry that is MISSING them.
type MCPApproval struct {
	WorkspacePath string `json:"workspacePath"`
	CollectionID  string `json:"collectionId"`
	RequestID     string `json:"requestId"`
	// EnvironmentID is the selected collection environment; "" means none.
	EnvironmentID string `json:"environmentId"`
	// GlobalEnvironmentIDs is the ordered list of active global environments.
	// Always written as an array, never null, for the reason above.
	GlobalEnvironmentIDs []string `json:"globalEnvironmentIds"`
	// Origin is canonical scheme://host:port.
	Origin string `json:"origin"`
	// KindClass is request | token | aws.
	KindClass string `json:"kindClass"`
	// ApprovedAt is for the user reading the file; nothing keys off it.
	ApprovedAt time.Time `json:"approvedAt"`
}

// MCPApprovalFile is the on-disk shape of the remembered approvals.
//
// Version is what makes the store safe to change. A file this build does not
// recognise is IGNORED rather than interpreted — an approval written under an
// older, wider key must never authorize anything under the narrower one — and
// the original is renamed aside rather than deleted. See
// mcpApprovalStoreVersion in internal/core/mcp_approvals.go.
type MCPApprovalFile struct {
	Version   int           `json:"version"`
	Approvals []MCPApproval `json:"approvals"`
}
