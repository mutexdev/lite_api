// describe_usage: the guide an agent reads instead of the document.
//
// WHY IT IS GO DATA AND NOT A MARKDOWN BLOB. docs/mcp-agent-interface.md is
// written for a person deciding whether to trust this feature; what an agent
// needs is a structure it can index — which rule forbids what, which tier is
// live right now, what a flow's fields mean, what a refusal will say before it
// is refused. Assembling it here also means the compiler sees it: the flow
// example below is a FlowDefinition, so a change to the schema an agent must
// send breaks this file rather than leaving a stale example in prose.
//
// IT IS WRITTEN FOR AN AGENT THAT HAS NEVER SEEN THE DOCUMENT. Nothing here
// says "see the docs"; every rule states what is enforced, and every refusal it
// predicts says what the agent should do instead — which is almost always "ask
// the user", because the things this server refuses are the things only the
// user can authorise.
package mcpserver

// usageGuide is the whole describe_usage payload.
type usageGuide struct {
	Server      usageServer      `json:"server"`
	SafetyRules []usageRule      `json:"safetyRules"`
	Tiers       []usageTier      `json:"tiers"`
	Authoring   usageAuthoring   `json:"authoring"`
	Flows       usageFlowSchema  `json:"flows"`
	Conventions []string         `json:"conventions"`
	Errors      usageErrorAdvice `json:"errors"`
}

type usageServer struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Protocol    string `json:"protocol"`
	Description string `json:"description"`
}

type usageRule struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Rule    string `json:"rule"`
	ForYou  string `json:"whatThisMeansForYou"`
	Enforce string `json:"enforcedBy"`
}

type usageTier struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Tools   []string `json:"tools"`
	Note    string   `json:"note"`
}

type usageAuthoring struct {
	NoScripts        usageAuthoringRule `json:"noScripts"`
	NoSecrets        usageAuthoringRule `json:"noSecretDefinitions"`
	HostApproval     usageAuthoringRule `json:"newHostApproval"`
	SettingsAreUsers usageAuthoringRule `json:"transportSettingsAreTheUsers"`
	RenameAndMove    usageAuthoringRule `json:"renamingAndMoving"`
}

type usageAuthoringRule struct {
	Rule      string `json:"rule"`
	Why       string `json:"why"`
	OnRefusal string `json:"ifYouAreRefused"`
}

type usageFlowSchema struct {
	WhatIsAFlow string         `json:"whatIsAFlow"`
	Fields      []usageField   `json:"fields"`
	Semantics   []string       `json:"semantics"`
	Example     FlowDefinition `json:"example"`
	ExampleNote string         `json:"exampleNote"`
	Validation  []string       `json:"validationRules"`
}

type usageField struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description"`
}

type usageErrorAdvice struct {
	Style   string   `json:"style"`
	Denied  string   `json:"denied"`
	Retries []string `json:"whenNotToRetry"`
}

// toolDescribeUsage answers with the guide, with the write tier's CURRENT
// state. The one live read is deliberate: an agent that is told the tier is on
// and then refused has been misinformed, and an agent told it is off knows to
// ask the user rather than to compose a call that cannot succeed.
//
// A backend that cannot answer does not fail the call. The guide is almost
// entirely static and is most useful exactly when something else is wrong; the
// tier is reported as off, which is the safe reading and the one that leads the
// agent to ask.
func toolDescribeUsage(backend Backend, _ toolArgs) (any, error) {
	writeEnabled, err := backend.WriteTierEnabled()
	if err != nil {
		writeEnabled = false
	}
	return buildUsageGuide(writeEnabled), nil
}

func buildUsageGuide(writeEnabled bool) usageGuide {
	return usageGuide{
		Server: usageServer{
			Name:     ServerName,
			Version:  ServerVersion,
			Protocol: ProtocolVersion,
			Description: "LiteAPI is an API client the user already keeps their collections, environments, auth and TLS settings in. " +
				"This server lets you use those definitions the way the user does: read them, run them through LiteAPI's own transport " +
				"so credentials resolve inside the app and never reach you, and — when the user unlocks it — author new requests and flows. " +
				"Prefer running a stored request over rebuilding the call yourself: a hand-built call cannot see the user's secrets and will " +
				"not match what the app sends.",
		},
		SafetyRules: usageSafetyRules(),
		Tiers:       usageTiers(writeEnabled),
		Authoring:   usageAuthoringRules(),
		Flows:       usageFlows(),
		Conventions: []string{
			"Every id a tool returns is accepted by every tool that takes an id of that kind: a collectionId from list_collections, a requestId from list_requests, search_requests or create_request, a flowId from list_flows or create_flow.",
			"List tools take an optional limit and return a stable order, so the same call twice gives the same rows in the same order.",
			"Request definitions are returned AS AUTHORED. {{templates}} are never resolved, and you must keep them that way when you write a definition back.",
			"Response bodies, request bodies, scripts and tests pass through in full and are NOT scanned for credentials — a credential the user typed literally into a body or a script is visible to you. Never repeat one back; tell the user to move it into a variable.",
			"Sizes are bounded: one run's response body and one history entry's body are truncated with a truncated flag rather than streamed in full.",
			"Nothing you can call reveals a secret value, including error messages. Asking again in a different way will not change that.",
		},
		Errors: usageErrorAdvice{
			Style:  "Every error names the field that was wrong and what to pass instead. An error is addressed to you and is meant to be acted on, not surfaced verbatim to the user.",
			Denied: "An error that begins with \"denied\" is a guard holding, not a fault. It means a rule stopped the call: the write tier is off, a secret override was attempted, or a credential would have travelled to a host the user's collections have never sent it to. The user sees denials in LiteAPI's activity panel.",
			Retries: []string{
				"Do not retry a denial. Nothing about it is transient — the same call will be denied again.",
				"Do not work around a denial by rebuilding the request by hand, by pointing it at a different host, or by asking for the same thing through another tool.",
				"Tell the user what was refused and what they would have to do (enable the write tier in Settings → AI access, approve the host in the prompt LiteAPI raises, or point you at the right host), then wait.",
			},
		},
	}
}

func usageSafetyRules() []usageRule {
	return []usageRule{
		{
			Number:  1,
			Title:   "Secrets never cross this boundary",
			Rule:    "Request definitions come back with their {{templates}} unresolved. A secret variable is listed as a name with secret:true and an always-empty value. Credential-shaped literals are masked to \"<masked>\" in header rows, param rows, auth rows and the URL's query string.",
			ForYou:  "You can see WHICH credential a request uses and never WHAT it is. That is enough to run it: call run_request and the value resolves inside LiteAPI at send time.",
			Enforce: "Enforced in the app's Go process, not by these tool descriptions.",
		},
		{
			Number:  2,
			Title:   "Sent-request echoes show templates",
			Rule:    "When a tool reports what was sent, values that came from a secret appear in their {{template}} form, never as the resolved bytes.",
			ForYou:  "A run report showing \"Bearer {{apiToken}}\" means the real token did travel — to the host — and was kept from you. It is not a failure to resolve.",
			Enforce: "Enforced in the app's Go process.",
		},
		{
			Number:  3,
			Title:   "Response headers are redacted, bodies are not",
			Rule:    "Authorization, Proxy-Authorization, Set-Cookie, Cookie and any header matching *api-key* or *token* are masked in responses. Response bodies, request bodies, scripts and tests pass through in full.",
			ForYou:  "The data you came for is intact. If a body contains something credential-shaped, treat it as sensitive: do not echo it, and tell the user it is there.",
			Enforce: "Enforced in the app's Go process.",
		},
		{
			Number:  4,
			Title:   "The new-host guard",
			Rule:    "Every secret variable has a host allowlist learned from the requests that already use it. Anything you do that would send that secret somewhere else — a run with an overridden variable, a flow step that retargets one, or a request you author — pauses for the user's approval in the app and fails if they decline or do not answer.",
			ForYou:  "Point requests at the hosts the collection already uses. If you genuinely need a new host, ask the user; you cannot approve it and there is no way around it.",
			Enforce: "Enforced in the app's Go process, before anything is sent and before anything is saved.",
		},
		{
			Number:  5,
			Title:   "The write tier is off by default",
			Rule:    "create_request, update_request, create_flow and update_flow are listed whether or not the user has unlocked writing, and are refused while it is locked. Even unlocked, you can reference a secret variable by name and can never read or define one.",
			ForYou:  "The tools being listed is not permission. If one is refused, ask the user to turn on \"Allow AI tools to create and edit requests\" in LiteAPI's Settings → AI access.",
			Enforce: "Enforced in the app's Go process, read fresh on every call — the user can turn it off while you work.",
		},
		{
			Number:  6,
			Title:   "Everything is audited",
			Rule:    "Every tool call is recorded with its name, a summary of its arguments, its outcome (ok, denied, error) and a timestamp, and the user reads them in the app.",
			ForYou:  "Probing is visible. So is a denial, which shows as a denial rather than as an error.",
			Enforce: "Recorded by the server for every call, including the ones that failed.",
		},
	}
}

func usageTiers(writeEnabled bool) []usageTier {
	writeNote := "OFF right now. These four tools are listed but every call is refused. Only the user can unlock it, in LiteAPI's Settings → AI access (\"Allow AI tools to create and edit requests\"); ask them, and do not try to work around it."
	if writeEnabled {
		writeNote = "ON right now. You may author requests and flows, subject to the authoring rules below: no scripts, no secret definitions, and a new host for a secret needs the user's approval. The user can turn this off at any time."
	}
	return []usageTier{
		{
			Name:    "read",
			Enabled: true,
			Tools:   []string{"list_collections", "list_requests", "search_requests", "get_request", "list_environments", "list_flows", "get_flow", "get_history", "describe_usage"},
			Note:    "Always on while the server is running. Start with list_collections or search_requests; get_history shows real response shapes without making a network call.",
		},
		{
			Name:    "run",
			Enabled: true,
			Tools:   []string{"run_request", "run_flow"},
			Note:    "Executes the user's own requests through the user's own send path — their auth, TLS posture, client certificates, scripts and tests. Secrets resolve inside LiteAPI. Subject to the new-host guard.",
		},
		{
			Name:    "write",
			Enabled: writeEnabled,
			Tools:   []string{"create_request", "update_request", "create_flow", "update_flow"},
			Note:    writeNote,
		},
	}
}

func usageAuthoringRules() usageAuthoring {
	return usageAuthoring{
		NoScripts: usageAuthoringRule{
			Rule:      "You cannot author or edit a pre-request script, a post-response script, or a tests block. create_request refuses a definition that carries one. update_request PRESERVES the stored ones: pass them back byte-for-byte as get_request returned them, or leave them out entirely, and anything else is refused.",
			Why:       "Scripts run inside the user's own engine and can rewrite a request after the new-host guard has already checked it — a script could retarget a credential past the guard. The guard's guarantee only holds while scripts are written by the user.",
			OnRefusal: "Ask the user to write the script in the app if one is genuinely needed. Do not try to express the same logic as a body, a header or a flow step that smuggles code.",
		},
		NoSecrets: usageAuthoringRule{
			Rule:      "No row you author — header, param, path param, form field or variable — may be marked secret:true. Referencing a secret is free: write {{apiToken}} anywhere a value goes and LiteAPI resolves it at send time.",
			Why:       "Defining a secret would let you decide what a credential IS, which inverts the boundary that keeps you from reading one.",
			OnRefusal: "Drop the secret flag and reference an existing secret variable by name. list_environments shows which names exist. If the user needs a new secret, they create it in the app.",
		},
		HostApproval: usageAuthoringRule{
			Rule:      "Before a request you author is saved, LiteAPI works out which hosts it would aim each referenced secret at, under every environment the collection defines. Any (secret, host) pair the collections have never used raises an approval prompt in the app, and the save is refused if the user declines or nobody answers.",
			Why:       "Otherwise authoring would defeat the run-time guard: a request you wrote pointing {{apiToken}} at a host of your choosing would teach the allowlist that the host is legitimate, and every later run to it would pass unchecked.",
			OnRefusal: "Reuse the hosts already in the collection — usually by writing {{baseUrl}} or whichever variable list_environments reports — or ask the user to approve the host. A request that references no secret, or whose URL does not resolve to a host yet, saves without any prompt.",
		},
		SettingsAreUsers: usageAuthoringRule{
			Rule:      "Transport settings — TLS verification, redirect policy, timeouts — are not authorable. A request you create takes LiteAPI's defaults, which verify TLS.",
			Why:       "Those settings decide how safely a request travels and belong to the user, not to the agent composing the call.",
			OnRefusal: "Ask the user to change the setting in the app if a request genuinely needs a different posture.",
		},
		RenameAndMove: usageAuthoringRule{
			Rule:      "update_request cannot rename a request or move it between folders. Choose the name and the folder when you create it.",
			Why:       "A request's name is its file on disk and its identity in the user's tree; renaming and moving are the user's actions in the app.",
			OnRefusal: "Ask the user to rename or move it.",
		},
	}
}

func usageFlows() usageFlowSchema {
	return usageFlowSchema{
		WhatIsAFlow: "A Flow is a named, ordered chain of one collection's own requests, with values wired out of each response into the next, assertions, and declared outputs. It is stored in the collection alongside the requests and runs identically from the app's Flow tab and from run_flow. Prefer running a flow the user already wrote over stitching run_request calls together yourself.",
		Fields: []usageField{
			{Path: "id", Type: "string", Description: "Stable id. Omit it on create_flow and one is assigned; update_flow requires it."},
			{Path: "name", Type: "string", Required: true, Description: "What the flow is called. Required, and it must not collide with the name of a secret variable."},
			{Path: "description", Type: "string", Description: "One line on what the chain does."},
			{Path: "inputs", Type: "array", Description: "Values the caller supplies: {name, required?, description?}. These are the only names run_flow's inputs argument accepts; an undeclared one is refused."},
			{Path: "steps", Type: "array", Required: true, Description: "The chain, in order. At least one step."},
			{Path: "steps[].id", Type: "string", Required: true, Description: "Names the step in run reports and in extraction errors. Unique within the flow."},
			{Path: "steps[].requestId", Type: "string", Required: true, Description: "A request in THIS collection, from list_requests. An unknown id is refused."},
			{Path: "steps[].vars", Type: "object", Description: "Flow-scoped variables for this step's resolution, as strings. Values are interpolated against flow scope only — inputs plus what earlier steps extracted."},
			{Path: "steps[].extract", Type: "array", Description: "Values pulled out of this step's response into flow scope: {name, from, path}. from is \"body\" (path is a JSONPath), \"header\" (path is the header name) or \"status\" (path unused)."},
			{Path: "steps[].assert", Type: "array", Description: "Checks against this step's response. {type:\"status\", equals:N} or {type:\"status\", in:[N,…]}; {type:\"body\", path:\"$.a.b\", equals:…|contains:\"…\"|exists:true}."},
			{Path: "outputs", Type: "array", Description: "What the flow hands back: {name, value}, where value is a template resolved against the final flow scope, e.g. {{terminalId}}."},
		},
		Semantics: []string{
			"Steps run IN ORDER and FAIL FAST: a failed assertion or a failed extraction stops the flow, naming the step and the check. A run that stopped at step 2 reports two steps, and that is the report that step 3 never ran — not a truncated answer.",
			"Each step's request runs with its full normal machinery — the user's auth, TLS posture, client certificates, pre/post scripts and tests — through the same path a Send in the app takes.",
			"A step's vars are interpolated against FLOW SCOPE ONLY: the flow's inputs and everything earlier steps extracted. They never read the environment.",
			"Because of that, {{secretName}} in a step var STAYS LITERAL through flow scope: it is passed on to the send path and resolved there, inside LiteAPI, or not at all. A flow therefore cannot carry a credential, and get_flow can return one as authored without leaking anything.",
			"An extraction or a flow name that shadows a secret variable's name is refused when the flow is saved and again when it runs — a flow-scoped name that shadowed a secret would decide what the request sends in place of the credential.",
			"The new-host guard applies to EVERY step, not once for the flow: a step whose vars retarget the host is checked exactly as an overridden run is.",
		},
		Example:     usageFlowExample(),
		ExampleNote: "The canonical chain: a GraphQL lookup feeds a create on a second API, whose response feeds an activation on a third, with each step asserting what it needs. Note that every requestId is a request that already exists in the collection, that vars reference values earlier steps extracted, and that outputs names what the caller gets back.",
		Validation: []string{
			"The flow needs a name and at least one step.",
			"Step ids are unique; input names are unique.",
			"Every requestId must name a request in the same collection.",
			"An extract needs a name, and a body extraction needs a parseable JSONPath; a header extraction needs the header name in path.",
			"A status assertion must say equals or in; a body assertion must say equals, contains or exists — a path with no check is refused rather than treated as always passing.",
			"No input, step var or extraction may take the name of a secret variable.",
		},
	}
}

// usageFlowExample is docs/mcp-agent-interface.md's worked example, as data.
// Typed as a FlowDefinition so it cannot drift from the shape create_flow
// actually accepts: a change to the schema stops this file compiling.
func usageFlowExample() FlowDefinition {
	return FlowDefinition{
		ID:          "flow_8f3k",
		Name:        "Provision POS terminal",
		Description: "GraphQL lookup -> create terminal on API B -> activate on API C",
		Inputs: []FlowInput{
			{Name: "storeCode", Required: true, Description: "Store short code, e.g. DHK-04"},
		},
		Steps: []FlowStep{
			{
				ID:        "lookup",
				RequestID: "req_graphql_store",
				Vars:      map[string]string{"code": "{{storeCode}}"},
				Extract: []FlowExtract{
					{Name: "storeId", From: "body", Path: "$.data.store.id"},
					{Name: "region", From: "body", Path: "$.data.store.region"},
				},
				Assert: []FlowAssert{{Type: "status", Equals: 200}},
			},
			{
				ID:        "createTerminal",
				RequestID: "req_apib_create_terminal",
				Vars:      map[string]string{"storeId": "{{storeId}}", "region": "{{region}}"},
				Extract:   []FlowExtract{{Name: "terminalId", From: "body", Path: "$.terminal.id"}},
				Assert: []FlowAssert{
					{Type: "status", In: []int{200, 201}},
					{Type: "body", Path: "$.terminal.state", Equals: "created"},
				},
			},
			{
				ID:        "activate",
				RequestID: "req_apic_activate",
				Vars:      map[string]string{"terminalId": "{{terminalId}}"},
				Assert:    []FlowAssert{{Type: "status", Equals: 200}},
			},
		},
		Outputs: []FlowOutput{{Name: "terminalId", Value: "{{terminalId}}"}},
	}
}
