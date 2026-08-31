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
	Server        usageServer              `json:"server"`
	SafetyRules   []usageRule              `json:"safetyRules"`
	NotGuaranteed []usageLimit             `json:"whatIsNotGuaranteed"`
	Unavailable   []usageRemovedCapability `json:"unavailableToAgentRuns"`
	Tiers         []usageTier              `json:"tiers"`
	Authoring     usageAuthoring           `json:"authoring"`
	Flows         usageFlowSchema          `json:"flows"`
	Conventions   []string                 `json:"conventions"`
	Errors        usageErrorAdvice         `json:"errors"`
}

// usageLimit is one thing the safety rules deliberately do NOT promise.
//
// WHY THIS IS IN THE PAYLOAD AT ALL. An agent that believes the boundary is
// stronger than it is makes worse decisions than one told the truth: it will
// treat a response body as safe to quote, or a same-origin call as
// unobservable, on the strength of a guarantee nobody made. Every entry here is
// a real limit of the shipped design, stated in the same words the user-facing
// document uses.
type usageLimit struct {
	Title     string `json:"limit"`
	Detail    string `json:"detail"`
	WhatYouDo string `json:"whatThisMeansForYou"`
}

// usageRemovedCapability is one capability a UI Send has that a run you start does
// not, with the exact refusal it produces — so an agent can recognise the
// refusal it is about to read rather than treating it as a transient failure.
type usageRemovedCapability struct {
	Capability string `json:"capability"`
	Why        string `json:"why"`
	WhatYouDo  string `json:"whatToDoInstead"`
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
	WhatIsAFlow    string         `json:"whatIsAFlow"`
	Fields         []usageField   `json:"fields"`
	JSONPathSubset usageJSONPath  `json:"jsonPathSubset"`
	Semantics      []string       `json:"semantics"`
	Example        FlowDefinition `json:"example"`
	ExampleNote    string         `json:"exampleNote"`
	Validation     []string       `json:"validationRules"`
}

// usageJSONPath is the exact path language extractions and body assertions
// accept.
//
// WHY IT IS SPELLED OUT RATHER THAN CALLED "JSONPath". The word implies a large
// language, most of which is rejected here, and an agent that writes
// $.items[*].id from memory gets a parse error naming a path it believed was
// standard. Stating the three accepted forms and the rejected ones costs a
// dozen lines and removes a whole category of failed authoring round-trips.
type usageJSONPath struct {
	Accepted  []string `json:"accepted"`
	Rejected  []string `json:"rejected"`
	Why       string   `json:"whyItIsSmall"`
	OnMiss    string   `json:"whenAPathMatchesNothing"`
	Rendering string   `json:"howAResolvedValueIsRendered"`
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
		SafetyRules:   usageSafetyRules(),
		NotGuaranteed: usageNotGuaranteed(),
		Unavailable:   usageRemovedCapabilities(),
		Tiers:         usageTiers(writeEnabled),
		Authoring:     usageAuthoringRules(),
		Flows:         usageFlows(),
		Conventions: []string{
			"Every id a tool returns is accepted by every tool that takes an id of that kind: a collectionId from list_collections, a requestId from list_requests, search_requests or create_request, a flowId from list_flows or create_flow.",
			"List tools take an optional limit and return a stable order, so the same call twice gives the same rows in the same order.",
			"Request definitions are returned AS AUTHORED. {{templates}} are never resolved, and you must keep them that way when you write a definition back.",
			"Response bodies, request bodies, scripts and tests pass through in full and are NOT scanned for credentials — a credential the user typed literally into a body or a script is visible to you. Never repeat one back; tell the user to move it into a variable.",
			"Sizes are bounded: one run's response body and one history entry's body are truncated with a truncated flag rather than streamed in full.",
			"Nothing you can call deliberately reveals a secret value, including error messages. Asking again in a different way will not change that — and see whatIsNotGuaranteed for where that promise stops.",
			"environmentId names a COLLECTION environment. Omitting it means NO collection environment applies — it does not fall back to whichever environment is selected in the app's window, because that selection is frontend state this server cannot read. The workspace's active global environment applies either way and cannot be selected per call. It is also part of the approval key, so the same request under a different environment asks the user again.",
			"A run you start is checked at every network destination it touches, against that request's own definition under that run's environment (safety rule 4). Running a stored request as authored needs no approval; overriding a variable so it points somewhere new does, whether or not a credential is involved.",
		},
		Errors: usageErrorAdvice{
			Style:  "Every error names the field that was wrong and what to pass instead. An error is addressed to you and is meant to be acted on, not surfaced verbatim to the user.",
			Denied: "An error that begins with \"denied\" is a boundary holding, not a fault. It means a rule stopped the call: the write tier is off, a value you supplied would have injected a credential, a capability is unavailable to agent-initiated runs (see unavailableToAgentRuns), or the run would have contacted a destination the request's own definition does not point at. A destination denial names the origin it refused and does NOT name a credential, because the check does not ask what the request carries. The user sees denials in LiteAPI's activity panel.",
			Retries: []string{
				"Do not retry a denial. Nothing about it is transient — the same call will be denied again.",
				"Do not work around a denial by rebuilding the request by hand, by pointing it at a different destination, by routing it through a script or a flow, or by asking for the same thing through another tool. Every one of those paths is checked the same way.",
				"Do not edit the user's collection files, environment files or LiteAPI data directory on disk to get past one. That is not a workaround; it is defeating a control the user is relying on.",
				"Tell the user what was refused and what they would have to do (enable the write tier in Settings → AI access, approve the destination in the prompt LiteAPI raises, run the request in the app for a capability agent runs do not have, or point you at the right request), then wait.",
			},
		},
	}
}

func usageSafetyRules() []usageRule {
	return []usageRule{
		{
			Number:  1,
			Title:   "Secrets never deliberately cross this boundary",
			Rule:    "Request definitions come back with their {{templates}} unresolved. A secret variable is listed as a name with secret:true and an always-empty value. Credential-shaped literals are masked to \"<masked>\" in header rows, param rows, auth rows and the URL's query string. Masking is BEST-EFFORT and has stated limits: a value under 8 bytes is not value-matched, and a credential a server echoes back encoded, split, hashed or inside a JWT is not recognised. LiteAPI does not intentionally expose a raw secret field to you; it does not guarantee that no secret ever reaches your output.",
			ForYou:  "You can see WHICH credential a request uses and never, deliberately, WHAT it is. That is enough to run it: call run_request and the value resolves inside LiteAPI at send time. If something credential-shaped does reach you anyway, treat it as the user's secret: do not echo it, do not store it, and tell them it leaked into a response.",
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
			ForYou:  "The data you came for is intact. There is deliberately NO body gating here: reading the data is the point, and a host the user's own requests already point at could defeat any such gate by re-encoding what it echoes. If a body contains something credential-shaped, treat it as sensitive: do not echo it, and tell the user it is there.",
			Enforce: "Enforced in the app's Go process.",
		},
		{
			Number:  4,
			Title:   "The destination boundary: a run reaches only where its own definition points",
			Rule:    "For a run you start, every network destination LiteAPI contacts is checked at the moment of contact against the origins THAT REQUEST'S stored definition resolves to under THAT run's environment, with none of your input applied — plus whatever the user has already approved for that exact request in that exact environment. An origin is scheme + host + port, so :3000 and :8080 are different destinations and http:// and https:// are different destinations. It covers the request itself, every redirect hop, anything a script sends or looks up, the OAuth2 token endpoint, and AWS credential endpoints. Anything else is blocked before the connection is opened, and raises an approval prompt in the app — or fails outright when there is no window to ask in.",
			ForYou:  "Run requests as authored. Overriding a variable so a request points somewhere new is the thing this stops, and it stops it whether or not a credential is involved — the check does not ask what the request carries. If you genuinely need a new destination, ask the user: you cannot approve it, retrying will not change it, and there is no other tool that reaches the network.",
			Enforce: "Enforced in the app's Go process, at each egress, before the connection is made. Also asked before a save, because saving a request is what would teach the boundary a new destination.",
		},
		{
			Number:  5,
			Title:   "A value you supply may not inject a credential",
			Rule:    "Interpolation is multi-pass, so a value you pass is not inert: {\"smuggle\": \"{{apiToken}}\"} on a request whose header reads \"Bearer {{smuggle}}\" would resolve the real credential at send time. Any value you supply that resolves to a secret — by name, transitively through other variables, or by containing a known secret value outright — is refused. So is overriding a secret variable by name.",
			ForYou:  "There is no approval path for this one and no legitimate use for it: a request references a credential because the USER wrote that reference into the definition, so run the request as authored and let LiteAPI resolve it. Drop the variable and try again.",
			Enforce: "Enforced in the app's Go process, before the run starts.",
		},
		{
			Number:  6,
			Title:   "A run you start changes nothing on disk",
			Rule:    "Variable writes a script makes (bru.setVar) and cookies a response sets live for the duration of your execution — across a flow's steps — and are then discarded. They never reach the user's saved collections or environments.",
			ForYou:  "A flow still works: step 1's setVar is visible to step 3. What does not happen is your run leaving state behind for the next one, so do not use a run to set something up for a later call. The user's own Send in the app is unaffected and still persists normally.",
			Enforce: "Enforced in the app's Go process, structurally: the write simply does not happen.",
		},
		{
			Number:  7,
			Title:   "The write tier is off by default",
			Rule:    "create_request, update_request, create_flow and update_flow are listed whether or not the user has unlocked writing, and are refused while it is locked. Even unlocked, you can reference a secret variable by name and can never read or define one.",
			ForYou:  "The tools being listed is not permission. If one is refused, ask the user to turn on \"Allow AI tools to create and edit requests\" in LiteAPI's Settings → AI access.",
			Enforce: "Enforced in the app's Go process, read fresh on every call — the user can turn it off while you work.",
		},
		{
			Number:  8,
			Title:   "Everything is audited",
			Rule:    "Every tool call is recorded with its name, a summary of its arguments, its outcome (ok, denied, error) and a timestamp, and the user reads them in the app.",
			ForYou:  "Probing is visible. So is a denial, which shows as a denial rather than as an error.",
			Enforce: "Recorded by the server for every call, including the ones that failed.",
		},
	}
}

// usageNotGuaranteed is the honest half of the safety rules, and it is not
// optional garnish.
//
// Every entry is a limit the design accepted on purpose, with the reasoning
// stated where the reasoning is what makes the limit defensible rather than an
// oversight. An agent that reads only the rules would conclude that a
// credential cannot reach it and that an approved destination is a safe place
// to send anything; both are false, and acting on either is how a boundary that
// holds gets blamed for a leak it never claimed to prevent.
func usageNotGuaranteed() []usageLimit {
	return []usageLimit{
		{
			Title:     "No credential confidentiality against a destination the user already trusts",
			Detail:    "The guarantee is about DESTINATIONS: a credential cannot be sent somewhere the user's own definitions do not point. It is not confidentiality. A host a request already points at receives that request's credential by design, and can echo it back in any form — base64ed, split across fields, hashed, inside a JWT — that no masker recognises. Shaping a request to an allowed origin (its path, query, body or method) may induce exactly that.",
			WhatYouDo: "Do not treat a response body from an allowed origin as guaranteed free of the user's credentials. If you see something credential-shaped, stop, do not repeat it, and tell the user.",
		},
		{
			Title:     "Masking is best-effort, and its limits are specific",
			Detail:    "Values under 8 bytes are not value-matched, because masking a 4-character string would corrupt every port number and status code in a body. A credential that is encoded, split or transformed before it appears is not matched. A dynamic token that equals no known secret is not masked at all. LiteAPI does not intentionally expose a raw secret field through any tool schema, and applies exact-value masking on top of name-based redaction — but it does not guarantee that no secret ever reaches your output.",
			WhatYouDo: "Treat masking as a courtesy, not a filter you can rely on.",
		},
		{
			Title:     "Same-origin totality",
			Detail:    "An origin that is in a request's Base, or that the user approved for it, is authorised for every path, method and body on that request. There is no per-path or per-method authority.",
			WhatYouDo: "Do not read \"this run was allowed\" as \"this particular call was reviewed\". Nobody reviewed the path you chose.",
		},
		{
			Title:     "DNS and other resolver traffic is outside the boundary",
			Detail:    "The guarantee covers application-layer egress and explicitly EXCLUDES resolver traffic. Resolving a hostname is not an authorised destination and is not checked as one; a name lookup can therefore reach a resolver, and the name itself is visible to it. Script-initiated lookups ARE checked against the run's own hostnames, but the ordinary resolution the transport performs is not.",
			WhatYouDo: "Nothing you can do about it, and nothing to route around. It is stated so that \"zero bytes left the machine\" is not read as more than it says.",
		},
		{
			Title:     "Network identity is syntactic",
			Detail:    "An origin is compared as scheme, host and port as written. DNS answers, /etc/hosts, a compromised proxy and TLS interception are not defended against — the same hostname can be made to resolve anywhere.",
			WhatYouDo: "Nothing. Stated for completeness.",
		},
		{
			Title:     "Proxies the process was launched with are trusted configuration",
			Detail:    "The OS system proxy, the HTTPS_PROXY/HTTP_PROXY/NO_PROXY environment variables, and the user's own manually configured proxy all physically carry this traffic. You cannot select or alter any of them through this server, which is why they are trusted — not because they are verified.",
			WhatYouDo: "Nothing. Stated so the guarantee is not read as \"traffic reaches only the named origin\".",
		},
		{
			Title:     "Anything with write access to the user's files defeats all of this",
			Detail:    "The boundary assumes this server is your only channel into LiteAPI's state. Collection files, the data directory (including the approvals store), preferences and the binary are ordinary files owned by the same user. Editing a collection file directly is a way to change what a run is allowed to contact, and no in-app check sees it.",
			WhatYouDo: "Do not edit the user's collection files, environments or LiteAPI data directory on disk to work around a refusal. That is not a clever route to the same result; it is defeating a control the user is relying on.",
		},
		{
			Title:     "An origin that legitimately received data can forward it",
			Detail:    "Authorising a destination says nothing about what that destination does next.",
			WhatYouDo: "Nothing enforceable. Worth remembering when you choose which stored request to run.",
		},
		{
			Title:     "A token fetched during your run is cached process-wide",
			Detail:    "An OAuth2 token obtained during a run you started — from an endpoint that was checked — is cached and may later serve the user's own Send, and vice versa. No new network egress happens in either direction, and it cannot widen what any run is allowed to contact.",
			WhatYouDo: "Nothing. Stated because it is the one persistent effect a run deliberately leaves behind.",
		},
	}
}

// usageRemovedCapabilities is the list of things a UI Send can do that a run you
// start cannot, each with the refusal it produces.
//
// AN AGENT THAT KNOWS THIS LIST STOPS SOONER. Every one of these produces a
// refusal that no retry and no rephrasing will change, and each has exactly one
// resolution: the user does it in the app. Telling an agent afterwards is worse
// than telling it up front — by then it has usually tried three variations.
func usageRemovedCapabilities() []usageRemovedCapability {
	return []usageRemovedCapability{
		{
			Capability: "Saving variable changes and cookies",
			Why:        "A run you start keeps script and response variable changes in memory for the duration of the execution — a whole flow, including across its steps — and then discards them. Nothing reaches the user's saved collections, environments or cookie jar. This is not an error and you will not see a refusal; the run simply changes nothing on disk.",
			WhatYouDo:  "Do not use one run to set up state for another. Pass what you need as variables, or ask the user to run it in the app if the state genuinely has to persist.",
		},
		{
			Capability: "AWS profiles that use credential_process",
			Why:        "Resolving one runs an external program of the profile's choosing, with whatever network access that program has. That is not an egress this boundary can reason about, so it is refused before the process is spawned.",
			WhatYouDo:  "Ask the user to run the request in the LiteAPI app, or to switch the profile to static keys or SSO. credential_source=environment is unaffected.",
		},
		{
			Capability: "gRPC targets that are not a plain TCP authority",
			Why:        "Unix sockets, abstract sockets and alternate grpc resolvers (unix:, xds:, dns:// and friends) have no origin to check, and instantiating the resolver is itself the side effect. Only host:port, grpc:// and grpcs:// targets are accepted, and the target is pinned explicitly before any dial.",
			WhatYouDo:  "Ask the user to run that request in the app. A gRPC request aimed at host:port works normally.",
		},
		{
			Capability: "PAC (proxy auto-config) proxies",
			Why:        "A PAC file is a JavaScript program with its own DNS and its own fetch, so evaluating one is an unbounded egress before any destination has been decided. Any effective configuration that resolves to PAC is refused before the file is fetched.",
			WhatYouDo:  "Ask the user to run it in the app, or to switch the proxy setting to manual or system.",
		},
		{
			Capability: "OAuth2 grants that need a browser sign-in",
			Why:        "authorization_code and implicit open a browser window and wait for a human. A run you start cannot, so it is refused — but only when nothing else can serve: a valid cached token, or an expired one with a usable refresh token, is used silently first.",
			WhatYouDo:  "Ask the user to open the request in LiteAPI and fetch the token once. Your runs then use the cached token and its refresh token automatically.",
		},
		{
			Capability: "A client certificate together with an HTTPS proxy",
			Why:        "The certificate could be presented to the proxy rather than only to the destination, and Go offers no way to withhold it per host.",
			WhatYouDo:  "Ask the user to run it in the app.",
		},
		{
			Capability: "Following a redirect away from a client-certificate request",
			Why:        "A certificate loaded for one origin sits on the transport for every host it dials, so a redirect elsewhere could present it to the redirect target. This one is refused rather than offered for approval.",
			WhatYouDo:  "Ask the user to run it in the app.",
		},
		{
			Capability: "Choosing which client certificate or proxy a request uses",
			Why:        "Certificate matching and proxy resolution are done with the request's own stored values, ignoring anything you supply, so a variable you pass cannot change which certificate is presented or which proxy is used.",
			WhatYouDo:  "Nothing to do. Your variables still shape the request itself.",
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
			Tools:   []string{"list_collections", "list_requests", "search_requests", "get_request", "inspect_request", "list_environments", "list_flows", "get_flow", "get_history", "describe_usage"},
			Note:    "Always on while the server is running. Start with list_collections or search_requests; inspect_request shows how a request would actually execute (inherited auth and headers, which variables resolve and which do not) without sending anything; get_history shows real response shapes without making a network call.",
		},
		{
			Name:    "run",
			Enabled: true,
			Tools:   []string{"run_request", "run_flow"},
			Note:    "Executes the user's own requests through the user's own send path — their auth, TLS posture, client certificates, scripts and tests. Secrets resolve inside LiteAPI. Every network destination the run touches is checked against the request's own definition (safety rule 4), and four capabilities a UI Send has are unavailable to you (safety rule 7's list).",
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
			Why:       "A script runs inside the user's own engine and rewrites the request the user believes they authored. Its own network calls ARE checked like everything else, so this is not about egress — it is that an agent able to write a script could rewrite the user's definitions rather than merely run them, and a definition is what the whole boundary is computed from.",
			OnRefusal: "Ask the user to write the script in the app if one is genuinely needed. Do not try to express the same logic as a body, a header or a flow step that smuggles code.",
		},
		NoSecrets: usageAuthoringRule{
			Rule:      "No row you author — header, param, path param, form field or variable — may be marked secret:true. Referencing a secret is free: write {{apiToken}} anywhere a value goes and LiteAPI resolves it at send time.",
			Why:       "Defining a secret would let you decide what a credential IS, which inverts the boundary that keeps you from reading one.",
			OnRefusal: "Drop the secret flag and reference an existing secret variable by name. list_environments shows which names exist. If the user needs a new secret, they create it in the app.",
		},
		HostApproval: usageAuthoringRule{
			Rule:      "Before a request you author is saved, LiteAPI works out every origin it would reach — its own destination, its OAuth2 token endpoint, its AWS credential endpoints — under every environment the collection defines. Any origin no OTHER request in that same collection already reaches raises an approval prompt in the app, and the save is refused if the user declines or nobody answers. It is deliberately secret-blind: an origin is worth a question whatever the request happens to carry.",
			Why:       "Otherwise authoring would defeat the run-time boundary, which is computed FROM stored definitions: a request you wrote pointing at a destination of your choosing would teach that boundary the destination is legitimate for that request, and every later run to it would pass with no prompt at all. Another collection's destinations do not count, because an approval is scoped to a site and a site names the collection.",
			OnRefusal: "Reuse the destinations the collection already uses — usually by writing {{baseUrl}} or whichever variable list_environments reports — or ask the user to approve the origin. A request whose URL does not resolve to an origin yet saves without any prompt; it teaches the boundary nothing and cannot be sent until it resolves, at which point the run is checked.",
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
			{Path: "steps[].extract", Type: "array", Description: "Values pulled out of this step's response into flow scope: {name, from, path}. from is \"body\" (path is a JSONPath from the subset described under jsonPathSubset), \"header\" (path is the header name) or \"status\" (path unused)."},
			{Path: "steps[].assert", Type: "array", Description: "Checks against this step's response. {type:\"status\", equals:N} or {type:\"status\", in:[N,…]}; {type:\"body\", path:\"$.a.b\", equals:…|contains:\"…\"|exists:true}. A body assertion's path uses the same subset as an extraction."},
			{Path: "outputs", Type: "array", Description: "What the flow hands back: {name, value}, where value is a template resolved against the final flow scope, e.g. {{terminalId}}."},
		},
		JSONPathSubset: usageJSONPath{
			Accepted: []string{
				"$ — the root, and it is required. A bare \"$\" is refused: name a value inside the document.",
				"Dot property access, chained: $.data.store.id",
				"A bracketed quoted key, for names a dot cannot express: $[\"key with spaces\"] or $['key with spaces']",
				"A non-negative integer array index: $.data.items[0].id",
			},
			Rejected: []string{
				"Wildcards: $.items[*].id and $.* are refused at parse time.",
				"Recursive descent: $..id is refused at parse time.",
				"Filter expressions: $.items[?(@.active)] is refused.",
				"Slices: $.items[1:3] is refused.",
				"Negative indexes: $.items[-1] is refused.",
				"Functions and anything else not listed under accepted.",
			},
			Why:       "A flow variable holds exactly ONE value, and every rejected form names either zero values or many. Accepting $.items[*].id would mean inventing a rule the flow's author never wrote — \"the first one\" — and quietly carrying a value they did not choose into the next request. Rejecting it, with the offending path quoted, is the honest answer.",
			OnMiss:    "A path that parses but resolves to nothing FAILS THE STEP and reports how far it did resolve. It never yields an empty string, because a flow that silently carried \"\" from a lookup into the body of the next request would send a wrong request to a real API and report success.",
			Rendering: "A string renders unquoted, so a token extracted from a body can be pasted straight into the next request's header. A number renders byte-for-byte as the server sent it, with no float rounding, so a large id survives. An object or array renders as compact JSON, which is what makes $.filter usable as a whole sub-document to post onward.",
		},
		Semantics: []string{
			"Steps run IN ORDER and FAIL FAST: a failed assertion or a failed extraction stops the flow, naming the step and the check. A run that stopped at step 2 reports two steps, and that is the report that step 3 never ran — not a truncated answer.",
			"Each step's request runs with its full normal machinery — the user's auth, TLS posture, client certificates, pre/post scripts and tests — through the same path a Send in the app takes.",
			"A step's vars are interpolated against FLOW SCOPE ONLY: the flow's inputs and everything earlier steps extracted. They never read the environment.",
			"Because of that, {{secretName}} in a step var STAYS LITERAL through flow scope: it is passed on to the send path and resolved there, inside LiteAPI, or not at all. A flow therefore cannot carry a credential, and get_flow can return one as authored without leaking anything.",
			"An extraction or a flow name that shadows a secret variable's name is refused when the flow is saved and again when it runs — a flow-scoped name that shadowed a secret would decide what the request sends in place of the credential.",
			"The destination boundary applies to EVERY step, not once for the flow, and each step is judged against ITS OWN request's definition: a step may not contact an origin merely because an earlier step did, and a step whose vars retarget it is checked exactly as an overridden run is.",
		},
		Example:     usageFlowExample(),
		ExampleNote: "The canonical chain: a GraphQL lookup feeds a create on a second API, whose response feeds an activation on a third, with each step asserting what it needs. Note that every requestId is a request that already exists in the collection, that vars reference values earlier steps extracted, and that outputs names what the caller gets back.",
		Validation: []string{
			"The flow needs a name and at least one step.",
			"Step ids are unique; input names are unique.",
			"Every requestId must name a request in the same collection.",
			"An extract needs a name, and a body extraction needs a path from the accepted subset above; a header extraction needs the header name in path.",
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
