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
// SECRET NAMES, NEVER VALUES. SecretNames says which credentials are involved,
// because that is what makes the decision feel real to the person answering.
// For the DESTINATION subject it is advisory text only — nothing keys an
// approval on it and no enforcement consults it. For the FLOW STEP VAR subject
// below it is part of the key, because "may this variable be allowed to reach
// apiToken" is the whole question being asked.
//
// TWO SUBJECTS, ONE PAYLOAD, DISCRIMINATED BY Subject. The destination prompt
// asks about an ORIGIN; the flow-step-var prompt asks whether a stored step var
// may resolve to a credential while the write tier makes its authorship
// ambiguous. They are different sentences and different keys, and forcing the
// second through Origin/KindClass would put a lie in the data — so the payload
// carries both sets of fields and says which one it means.
//
// The frontend answers with App.ResolveMCPApproval(id, approve, remember).
type MCPApprovalRequest struct {
	ID string `json:"id"`
	// Subject is which question this prompt asks: MCPApprovalSubjectDestination
	// (the default, and what an empty value means) or
	// MCPApprovalSubjectFlowStepVar.
	Subject string `json:"subject,omitempty"`
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
	// SecretNames is the credentials involved: advisory for the destination
	// subject, part of the key for the flow-step-var one (see above).
	SecretNames []string `json:"secretNames,omitempty"`

	// --- the flow step var (Subject == MCPApprovalSubjectFlowStepVar only)
	//
	// FlowID, StepID and VarName are key fields; FlowName is a display name and
	// is not, for the same reason CollectionName is not. RequestID/RequestName
	// above name the request the var feeds, which is what makes the sentence
	// answerable — a variable name alone says nothing about where the credential
	// would end up.
	FlowID   string `json:"flowId,omitempty"`
	FlowName string `json:"flowName,omitempty"`
	StepID   string `json:"stepId,omitempty"`
	VarName  string `json:"varName,omitempty"`
}

// The values of MCPApprovalRequest.Subject.
const (
	// MCPApprovalSubjectDestination is "may this run contact this origin".
	// The empty string means this too: a payload from an older build, or one
	// built by a caller that predates the second subject, is a destination
	// prompt.
	MCPApprovalSubjectDestination = "destination"
	// MCPApprovalSubjectFlowStepVar is "may this flow step's var resolve to this
	// secret".
	MCPApprovalSubjectFlowStepVar = "flowStepVar"
)

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

// MCPStepVarApproval is one remembered "this flow step's var may resolve to
// these secrets, under this environment" decision. Persisted alongside the
// destination approvals in <dataDir>/mcp-approvals.json.
//
// A SECOND KIND RATHER THAN A REUSE OF MCPApproval, deliberately. That type is
// about an ORIGIN — where a run may send — and every field of it exists to name
// a destination. This one is about a VALUE: a stored step var whose braces
// resolve to a credential, which the write tier makes ambiguous in authorship
// (see internal/core/mcp_flows.go). Squeezing it into Origin/KindClass would
// mean writing a fake origin into a file whose whole job is to record exactly
// what the user was shown.
//
// EVERY FIELD IS PART OF THE KEY EXCEPT ApprovedAt, and every one narrows. An
// approval for one var does not speak for another var, another step, another
// flow, another secret, or the same flow under a different environment. The
// environment is in the key for the same reason it is in MCPApproval's: whether
// a NAME resolves to a secret, and to WHICH secret, depends on which
// environments are active, so a "yes" under dev must not answer for prod.
//
// SecretNames is the SORTED set of secrets this var was found to reach, and it
// is keyed as a set: a var that later reaches one more credential than the user
// approved produces a different key and therefore a fresh prompt, which is the
// conservative direction.
//
// NO OMITEMPTY ON THE KEY FIELDS, for the reason MCPApproval gives: an absent
// field and an empty one must stay distinguishable on reload, because the
// migration rule ignores an entry that is MISSING one.
type MCPStepVarApproval struct {
	WorkspacePath string `json:"workspacePath"`
	CollectionID  string `json:"collectionId"`
	FlowID        string `json:"flowId"`
	StepID        string `json:"stepId"`
	// VarName is the step var's name — the key in FlowStep.Vars.
	VarName string `json:"varName"`
	// SecretNames is the sorted set of secret names the var's value reaches.
	// Always written as an array, never null.
	SecretNames []string `json:"secretNames"`
	// EnvironmentID is the selected collection environment; "" means none.
	EnvironmentID string `json:"environmentId"`
	// GlobalEnvironmentIDs is the ordered list of active global environments.
	// Always written as an array, never null.
	GlobalEnvironmentIDs []string `json:"globalEnvironmentIds"`
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
//
// THE SECOND LIST NEEDED NO VERSION BUMP, and that is the version field working
// rather than being ignored. A Version 1 file written before step-var approvals
// existed simply carries none, and "none remembered" is the correct reading of
// it: nothing was ever approved, so nothing is authorized and the next run asks.
// A bump would have been required only if the meaning of an EXISTING entry had
// changed, and none did.
type MCPApprovalFile struct {
	Version   int           `json:"version"`
	Approvals []MCPApproval `json:"approvals"`
	// StepVarApprovals is the second kind. Its own list rather than a variant
	// row in Approvals: two shapes in one array is a file nobody can read
	// without a discriminator, and a discriminator is what separate lists are.
	StepVarApprovals []MCPStepVarApproval `json:"stepVarApprovals"`
}
