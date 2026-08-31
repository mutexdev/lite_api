# LiteAPI as an agent tool: the MCP interface

LiteAPI embeds an MCP (Model Context Protocol) server so that AI coding tools the
user already pays for — Claude Code, Codex, Cursor, anything that speaks MCP —
can use LiteAPI the way the user does: discover collections, run requests
through LiteAPI's own transport, run multi-step Flows, and (opt-in) author new
requests and Flows. LiteAPI itself gains no AI features and holds no AI keys.

The problem this solves: teams keep their API knowledge in collections. Agents
that cannot see them rebuild every call from scratch — slowly, wrongly, and at
token cost. With this interface an agent calls `search_requests` and
`run_request` and gets real data through requests the user already configured,
including auth, TLS settings, and client certificates.

## The two boundaries

There are two, they answer different questions, and confusing them is how a
reader ends up trusting the wrong thing.

- **The output boundary** decides what an agent gets to SEE: tool results,
  response bodies and headers, test results, history, error text. It is the
  redaction and masking layer, and it is best-effort by nature — the rules
  below say exactly where it stops.
- **The destination boundary** decides where an agent-initiated run gets to SEND:
  every application-layer egress of an MCP execution is authorized, at the
  egress, against the origins the request's own stored definition points at.
  This one is a stated, falsifiable guarantee, and the whole of it — including
  what it does NOT cover — is reproduced below from the design document.

A user pressing Send in the app is subject to neither.

## Non-negotiable safety rules

These rules bind every tool below. They are enforced in the Go process, not by
tool descriptions.

1. **Secrets never cross the MCP boundary.** Request definitions are returned
   with `{{templates}}` unresolved. Secret variables are listed as name +
   `secret: true`, value always masked. Resolution to real values happens only
   inside LiteAPI at send time. No tool, argument, or error message
   deliberately carries a resolved secret value. Credential-shaped *literals* —
   values a user typed in rather than templated — are masked wherever a request
   definition can carry one: header and param rows, auth block fields that are
   not merely addressing (a username, a client id, a token URL), and the query
   string of the URL itself, so a pasted `?api_key=sk_live_...` arrives as
   `?api_key=<masked>` with the rest of the URL byte-for-byte intact. What this
   rule does not promise is stated in §1.3 and §1.4(3): masking is best-effort,
   and a server the user already trusts can put a credential in a response body
   in a form no masker recognises.
2. **Sent-request echoes show templates.** When a tool reports what was sent,
   header/URL/body values that came from secret variables appear as their
   template form, never the resolved bytes.
3. **Response headers are redacted.** `Authorization`, `Proxy-Authorization`,
   `Set-Cookie`, `Cookie`, and headers matching `*api-key*`/`*token*`
   (case-insensitive) are masked in responses shown to the agent. Response
   bodies pass through in full — that is the data the agent is there for. So do
   request bodies, pre/post scripts and tests: they are returned exactly as
   authored and are NOT scanned for credentials, because a body mangled into
   something that no longer runs is worse than useless to an agent. A credential
   typed literally into a body or a script is therefore visible to the agent —
   keep real credentials in variables, which is the only place this boundary can
   protect them.
4. **The destination boundary.** An agent-initiated run may contact only the
   origins the request's own stored definition resolves to under the run's own
   environment, plus what the user has explicitly approved for that request in
   that environment. Everything else — a retargeted variable, a redirect to a
   new origin, a script calling out, an OAuth token endpoint the agent moved —
   is blocked at the egress, before the connection is made, and raises an
   approval prompt in the app (or fails outright with no window to ask in). The
   full statement, with its definitions and its explicit non-guarantees, is §1
   below; what an agent run can no longer do at all is §2.
5. **Write tier is off by default.** `create_*`/`update_*` tools are rejected
   until the user enables writes in Settings. Even then, agents can reference
   secret variables by name but can never read or define secret values.
6. **Everything an agent does is audited.** Every `tools/call` is recorded
   (tool, arguments summary, outcome, timestamp) and visible in the app's audit
   panel, with a refusal recorded as `denied` rather than as one more failure.
   Discovery calls — `initialize`, `tools/list`, `ping` — are not recorded:
   every client makes them on connect, and burying the calls that touched the
   user's data under that noise would make the panel useless.
7. **Agents cannot author scripts, and cannot define secrets.** A script runs
   inside the user's own engine and rewrites the request the user believes they
   authored; an agent that could write one could rewrite the user's definitions
   rather than merely run them. The write tier therefore refuses to author
   scripts or tests outright, preserves the ones a request already has, and
   refuses any authored row that declares itself secret. It also asks the
   destination question at SAVE time: because Base is computed from stored
   definitions, a request an agent saves pointing somewhere new would otherwise
   TEACH the boundary that the destination is legitimate, and every later run to
   it would pass unchecked. Authoring therefore raises the same approval prompt
   a run does, scoped to the owning collection — another collection's
   destinations are not this request's Base and cannot vouch for anything.
8. **An agent value may not inject a credential.** Interpolation is multi-pass,
   so a value an agent supplies is not inert data: `{"smuggle": "{{apiToken}}"}`
   on a request whose header reads `Bearer {{smuggle}}` would resolve the real
   credential at send time. Any agent-supplied value that resolves to a secret —
   by name, transitively, or by containing a known secret value outright — is
   refused, with no approval path, because there is no honest use for it. An
   agent that needs a credential runs the request the user already wrote. This
   covers what an agent AUTHORS as well as what it passes: `create_flow` and
   `update_flow` refuse a Flow step var whose value resolves to a secret, with no
   approval path, because an agent has no honest need to author one.

   **A STORED step var is a different question, and gets a different answer.** At
   RUN time a Flow records nothing about who wrote it, so while the write tier is
   on — when a step var that reaches a credential is one `update_flow` away from
   being the agent's — LiteAPI cannot tell the user's own from an agent's. That
   ambiguity is ASKED about rather than refused: `run_flow` raises an approval
   prompt naming the flow, the step, the variable, the secret and the request the
   variable feeds, and "allow and remember" is keyed on exactly that tuple plus
   the environment, so the user answers once per variable and never for anything
   else. A denial, a timeout, or a headless run with nobody to ask all refuse the
   run. With the write tier OFF nothing is asked at all: the agent has no
   authoring channel, so the step var is provably the user's and runs silently.

   The user's own Flow editor is unaffected throughout: aiming a secret at a
   request through a step var is what the Flow tier is for, and rule 8 is about
   the agent's read/write asymmetry, which the user does not have. Refusing a
   stored step var outright would have deleted that feature for anyone with the
   write tier on, which is why the run-time answer is a prompt.

---

The three sections that follow are reproduced **verbatim** from the Phase 6
design document. They are the statement of the guarantee, the list of what an
agent run can no longer do, and the ruling on the one attack this design
deliberately does not defend against. They are copied rather than paraphrased
because a security claim that is restated loses precisely the qualifications
that make it true.

Three editorial notes, outside the quoted text.

- The `file:line` references were accurate when the design was written and are
  kept as written. Several have since moved; the retired new-host guard's are
  gone entirely.
- The quoted sections cross-refer to numbered sections that are NOT reproduced
  here — §3 is the execution overlay (rule 6 above), §4 the mechanism, §5 the
  egress inventory, §6 the approval model, §7 the history projection. Where one
  of them carries something a reader needs, this document states it in its own
  words nearby; the numbers are left intact because renumbering a quoted text is
  how a quotation stops being one.
- §8's "retained read-boundary refusals" are today `mcpValidatedOverrides`
  (`internal/core/mcp_run.go`) and `mcpRefuseSecretInjectingValues`
  (`internal/core/mcp_guard.go`), the latter published above as rule 8.

## 1. The security property

### 1.1 Definitions

- **MCP-initiated execution E**: any call tree rooted in `mcpBackend.RunRequest` (`mcp_run.go:92`) or `mcpBackend.RunFlow`, GUI or headless. Wails bindings, the collection runner, imports, OpenAPI sync are user-initiated and out of scope.
- **Origin**: `(scheme, lowercased host, effective port)`. Effective-port rules: for `http`/`ws` the default is 80; for `https`/`wss` the default is 443; for gRPC targets — plaintext or TLS — the effective port when unspecified is **443**, matching grpc-go's DNS-resolver default and the explicit pin in §4.7 (the HTTP 80/443 scheme defaults do not apply to gRPC). Scheme normalization: `ws`->`http`, `wss`->`https`, plaintext gRPC->`http`, TLS gRPC->`https`; IPv6 hosts normalized. HTTP/WS origins are produced by `OriginOfURL`; gRPC origins are produced only by §4.7's validator, using its effective port. Replaces `mcpNormalizeHost` (`mcp_guard.go:95`).
- **Definition scope S**: the stored definition whose send is executing, `(collectionID, requestID)` — main request, current flow step, or current nested `bru.runRequest` target. Set per flow step; pushed/popped LIFO for nested sends; never accumulates (§4.1).
- **Egress kind k**: `main` (the request itself: HTTP/GraphQL, WS handshake, gRPC dial, digest retry), `redirect`, `script`, `script-dns`, `token` (non-interactive OAuth2), `aws`, `proxy`.
- **Base(S, k)**: origins from scope S's stored definition resolved with **the run's single agent-free variable context** — the exact `scripting.NewScriptVariableContext` construction `mcpRunPlan` performs at `mcp_run.go:210`, holding the selected collection environment and the currently active global-environment list — with no overrides, no flow inputs, no flow-extracted values, no execution-overlay values (§3 makes the last structural). This is never a union over environments: a run holding production credentials has exactly production's origins; a dev-only origin is outside Base and prompts. Per kind: `main` = the request URL resolved under that one context; `token` = effective OAuth2 access/refresh URL origins; `aws` = `awsv4.CredentialEndpointOrigins`; `proxy` = agent-free-resolved manual proxy origin; `redirect`/`script` = the scope's `main` set; `script-dns` = hostnames of all the above.
- **Site of S**: `(workspacePath, collectionID, requestID, environmentID, globalEnvironmentIDs)` where `environmentID` is the selected collection environment ("" = none) and `globalEnvironmentIDs` is the ordered list of active global environments. Environment identity is a LIST even though `ActiveGlobalEnvironmentsForWorkspace` (`internal/scripting/environments.go:16`) yields 0 or 1 today, so a future multi-active model cannot silently widen old approvals.
- **Approved(site, origin, class)**: persisted approvals keyed on the FULL site plus `(origin, kindClass)`, `kindClass` in {`request` (main/redirect/script/script-dns), `token`, `aws`}, plus in-execution allow-once (session) grants keyed with the identical shape (§6). Consequently: an approval for request A never authorizes request B; token-class never authorizes request-class; an approval remembered under dev never authorizes the same request under production, nor under a different active global-environment list — switching either changes the key and re-prompts. `proxy` has no approval path: the effective manual proxy equals the agent-free resolution by construction (§4.4).
- **Trusted-proxy set** — three distinct paths, each a physical egress the agent cannot select or alter through MCP:
  1. **The OS system proxy for LiteAPI request transports in System mode**, discovered per request by `SystemProxyURLForRequest` (`internal/transport/proxy.go:115`, engaged via `transport/cache.go:265-273`). Under MCP, §4.4 governs: a PAC disposition is refused, and for cert-bearing transports the discovered static disposition is frozen.
  2. **Process-environment proxies for the shared credential clients** (OAuth2, AWS): `sharedCredentialHTTPClient` is built with `ProxyInherit` (`http_transport_cache.go:118`), which leaves `http.DefaultTransport`'s `ProxyFromEnvironment` in place (`transport/cache.go:48-51`). These clients do NOT use system-mode discovery; they honor `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` directly.
  3. **grpc-go's own environment proxy**: grpc-go v1.81.1 (`go.mod:19`) itself reads `HTTPS_PROXY`/`NO_PROXY` via `http.ProxyFromEnvironment` and establishes its own CONNECT route to the proxy, independent of our HTTP stack entirely.

  Environment variables are set by whoever launched the process; the agent cannot alter them through MCP. The user's manually configured proxy (kind `proxy`, agent-free-resolved) completes the set.

### 1.2 The guarantee (falsifiable, and literally true)

> For every MCP-initiated execution E:
>
> 1. Every application-layer network egress the engine initiates as part of E, EXCLUDING resolver (DNS) traffic, is either (a) an HTTP(S)/WS(S)/gRPC-over-TCP egress whose target origin is, at the moment of egress, in `Base(S, k)` union `Approved(site(S), origin, class(k))` for the active definition scope S and egress kind k, or (b) a connection to a member of the trusted-proxy set. Any other egress is blocked before the connection or request is made. GUI: an approval prompt naming origin, definition scope, and egress kind. Headless, timeout, or denial: an `ErrDenied`-class error.
> 2. If E's effective configuration requires AWS `credential_process`, a gRPC target that is not a validated TCP authority (§4.7), a PAC proxy, or an interactive browser OAuth grant with no valid cached or refreshable token, E is refused at that feature's site, naming the feature and directing the user to run it in the app — before any subprocess spawn, socket dial or resolver instantiation, PAC fetch or evaluation, or browser open.
> 3. No script- or response-driven variable mutation and no cookie from E is written to `AppState` or disk. Consequently no agent-supplied value from any MCP execution can enter any later execution's `Base`.
> 4. A user-initiated send is never subjected to any of the above.

Testable: point the resolved destination at a listener whose origin is not in the set and assert zero bytes arrive; for refusal classes, assert zero subprocess spawn / socket dial / PAC fetch / browser open.

Engineering property backing clause 1 (§4.5): every egress through the wrapped HTTP clients carries explicit provenance (MCP policy or UI marker), attached at distinct roots via a required argument of an unexported type; unlabeled egress is refused once strict flips; provenance is never inferred from the absence of a policy.

### 1.3 Agent-facing output boundary, separately stated

Network egress: §1.2. Agent-facing output (tool results, bodies, headers, test results, history, errors, stdio): name-based redaction (`mcpserver/redact.go`), exact-value masking (`MaskKnownSecretValues`, mask-before-truncate), record-time history projection (§7). Limits: under 8 bytes not value-matched; encoded/split/transformed not matched; a dynamic token equal to no known secret is not masked. Limits of the output boundary, not the destination property — §1.4(3) states the consequence without euphemism.

### 1.4 Non-guarantees (verbatim in docs)

1. **Agent-writable files defeat everything.** Collection files, the data directory (including `mcp-approvals.json` — owner-only, outside every workspace at `a.dataDir`, `mcp_approvals.go:294`, but the same user's disk), preferences, or the binary written outside MCP: no in-app boundary holds. The property assumes MCP is the agent's only channel into LiteAPI state. Collection files and the approvals store do not necessarily have different filesystem permissions; the separation is of scope, not privilege.
2. **Same-origin totality.** An approved or base origin is authorized for every path, method and body; a `request`-class approval covers main, redirect and script egress to that origin for that request in that selected collection/global-environment configuration.
3. **No credential confidentiality against an allowed origin.** LiteAPI provides NO confidentiality guarantee for a credential against a host the user's own definitions already point at. Such a host can echo the credential back in any encoding, and an agent that shapes a request to an allowed origin (path, query, body, method) may induce that. The guarantee is about DESTINATIONS — a credential cannot be sent somewhere new — and about LiteAPI not intentionally exposing raw secret fields through MCP schemas (best-effort masking only; stored scripts can transform secrets past it). It is not perfect confidentiality. See §8.
4. **An origin that legitimately received data can forward it.**
5. **Network identity is origin-syntactic.** DNS, `/etc/hosts`, a compromised trusted proxy, TLS interception are not defended.
6. **The trusted-proxy set is trusted configuration.** The OS system proxy (discovered by `SystemProxyURLForRequest` for request transports in System mode), the process-environment proxy variables honored both by the shared credential clients via `ProxyInherit`/`http.DefaultTransport` (`http.ProxyFromEnvironment`) and by grpc-go itself (its own CONNECT route), and the user's manual proxy: engine traffic physically flows through these, and the agent cannot select or alter any of them through MCP.
7. **Browser navigation is outside the engine.** Moot for MCP (browser grants refused); stated for completeness.
8. **The mock server** is a user-opt-in unauthenticated loopback listener, outside this boundary. Tracked follow-up.
9. **Localhost is not special.** `:3000` and `:8080` are distinct origins (a fix).
10. **Concurrent definition edits** yield a spurious prompt or denial, never a silent widening.
11. **Output-boundary limits** as §1.3. Fresh tokens minted by an allowed server are agent-visible.
12. **An MCP run's OAuth token fetch** (from a checked endpoint) is cached and may later serve a UI send, and vice versa. No new egress either way.

## 2. What MCP runs can no longer do (vs a UI Send)

Refusal format: *feature — why unavailable to agent-initiated runs — the action*. Surfaces as the MCP tool-call error and, in GUI, an app notification. **Every refusal below is provenance-conditioned**: it fires only under MCP provenance, so a UI Send is unaffected. In particular both script-refusal belts in the send path are conditioned on provenance — `app_send.go:82` (gating pre-request `:121`, post-response variables `:147`, post-response script `:160`, tests `:172`) and `app_send.go:672` (gating nested sites `:687,:721,:727,:735`).

| # | Capability removed | Detection point | User-visible outcome |
|---|---|---|---|
| 1 | Persisting variable changes and cookies | send tail, `app_send.go:221-225` | No error. Changes live for the execution (flow steps included), then discarded. Result notes "variable changes from agent runs are not saved". |
| 2 | AWS profiles using `credential_process` | `profile.go:80`, before spawn | `AWS profile "x" uses credential_process, which runs an external program. Agent-initiated runs cannot use it. Run this request in the LiteAPI app, or switch the profile to static keys or SSO.` |
| 3 | gRPC targets other than a validated TCP authority | `mcpValidateGRPCTarget` (§4.7) before `grpcDialTarget` (`app_grpc.go:440`) | `This gRPC target is not a plain TCP authority. Agent-initiated runs can only use host:port, grpc://, or grpcs:// targets. Run it in the LiteAPI app.` Zero dial, zero resolver instantiation. |
| 4 | PAC proxies | `collectionProxyResolution().Mode == "pac"` (`app_execute_http.go:403`) before any PAC fetch; same on the WS proxy path; plus the in-closure and frozen-spec refusals of a system-proxy PAC disposition (§4.4) | `The effective proxy configuration uses a PAC file. Agent-initiated runs cannot evaluate PAC. Run this request in the app, or switch the proxy setting to manual or system.` |
| 5 | Interactive OAuth grants when no valid cached or refreshable token | grant branches (`app_oauth2.go:80,90`) — the cache/refresh check at `:59-77` runs first | `This request uses the OAuth2 <grant> grant, which needs a browser sign-in. Open the request in the LiteAPI app and fetch the token once; agent runs will then use the cached token (and its refresh token) automatically.` |
| 6 | Client certificate combined with an `https://` proxy (manual, or a discovered system proxy with an https scheme) | `requestTransport`, cert matched + https-scheme proxy disposition (§4.4) | `This request combines a client certificate with an HTTPS proxy; the certificate could be presented to the proxy. Run it in the app.` |
| 7 | Redirects off `certOrigin` when a client cert is loaded | guard transport, per hop (§4.4) | `Blocked a redirect to <origin>: this request carries a client certificate, which redirect targets could request. Run it in the app.` (Not approvable.) |
| 8 | Contacting origins outside `Base(S,k)` without approval | checkpoints + guard transport | GUI: approval prompt (§6); headless: `ErrDenied` naming origin, scope, kind, and how to pre-approve. |
| 9 | Agent variables influencing client-cert selection or proxy resolution | `requestTransport` uses agent-free inputs under MCP (§4.4) | No error; overrides simply not consulted for transport construction. |
| 10 | Blocking mid-redirect for approval | guard transport backstop | First cross-origin redirect to a new origin fails once with an actionable denial while a non-blocking prompt lets the user approve-and-remember; the retry succeeds. |

## 8. In-origin exfiltration: the ruling

The reviewer asked for response-body redaction or exposure gating whenever agent values shape a credential-bearing request. **This plan does not add body gating, by explicit product ruling, and the reasoning ships in the docs:**

The locked product requirement is that response bodies are fully visible — reading the data *is the feature*. Gating or redacting whenever any agent value shaped the request would fire on essentially every real run (agents parameterize everything) and destroys the product to defend a boundary that cannot be defended anyway: an allowed origin can echo a credential base64ed, split, hashed, or inside a JWT, and no record-time masking recognizes that. A control that is both product-destroying and circumventable is not a control.

The claim is written to be true instead (§1.4(3)): LiteAPI guarantees destinations and does not intentionally expose raw secret fields through MCP schemas, applying best-effort output masking, without guaranteeing a secret never reaches agent-visible output; it does NOT guarantee credential confidentiality against a host the user's own definitions already trust. The retained read-boundary refusals (`mcp_run.go:256`, `mcp_guard.go:513`) and exact-value masking are best-effort hardening against the lazy version of the attack, not a guarantee. A future opt-in `strictResponses` preference is noted as a follow-up.

---

## Transport and pairing

The server speaks MCP over streamable HTTP on `127.0.0.1:<port>` (default
must not collide with `wails dev`'s 34115) and requires a bearer token
generated per install. It runs only while the toggle in Settings → AI access
is on. Settings shows a copyable one-liner:

```
claude mcp add --transport http liteapi http://127.0.0.1:<port>/mcp --header "Authorization: Bearer <token>"
```

### Headless stdio: `liteapi mcp`

For a machine or a session where the desktop app is not running — a CI box, an
SSH session, an agent that would rather spawn a process than be handed a URL —
the same server runs over stdin/stdout:

```
liteapi mcp [--data-dir <path>]
```

```
claude mcp add liteapi -- /path/to/liteapi mcp
```

It is the app minus the window: the real data directory, the real state, the
real collections, the same Backend, guard, audit and preferences. The transport
is the MCP stdio transport — newline-delimited JSON-RPC, one message per line —
and it shares its dispatch with the HTTP handler, so a call answered over a pipe
returns the same bytes and records the same audit entry as the same call over
the port. **Stdout carries protocol and nothing else**; diagnostics go to
stderr.

**No bearer token.** Over HTTP the token separates the user's collections from
anything else that can reach the loopback interface. A pipe carries no such
ambiguity: stdin and stdout were handed to the process by the parent that
launched it, so possession of the pipe is the credential.

**One store, one writer.** If the app is open (or another `liteapi mcp` is
serving) over the same data directory, the subcommand refuses to start, says so
on stderr, and points at the running app's HTTP endpoint. It takes the same
per-workspace ownership lease the app window takes, so the refusal comes from
the lock itself rather than from a heuristic. A lease left behind by a killed
process expires within 30 seconds.

**The safety rules hold, with one consequence worth stating.** The destination
boundary still runs, and headlessly it *denies*: with no window there is nobody
to raise the approval prompt to, so any egress outside `Base(S, k)` fails with
the standard secret-free `denied:` message rather than pausing for an answer
that can never come. Destinations already approved in the app still work —
remembered approvals live in the same data directory and are read here too, and
because they are keyed on the full site (§6 of the design: workspace,
collection, request, selected environment, active globals, origin, kind class),
an approval given in the app applies here only to the same request under the
same environment. A run as authored needs no approval at all, which is what
keeps the headless mode useful rather than merely safe. Every call is audited
into the same log the app's panel reads. The write tier honours the same
off-by-default preference.

**The MCP enabled/port preferences do not apply.** They govern the HTTP
listener, which publishes a port other software on the machine can reach;
invoking the subcommand is itself the consent, so it serves regardless of the
Settings toggle and binds no port.

## Tool surface

Read tier (always on while the server is enabled):

| Tool | Purpose |
| --- | --- |
| `list_collections` | Collections with id, name, request/flow counts. |
| `list_requests` | Requests in a collection: id, name, method, URL template, folder path. |
| `search_requests` | Substring/keyword search across names, URLs, folder paths. An empty or omitted query matches everything. |
| `get_request` | Full definition (redacted): method, URL, headers, params, body, GraphQL variables, the *effective* auth mode plus the level that configured it (never the credentials), scripts, settings. |
| `inspect_request` | The request as it would actually execute: every inherited piece labelled with the level that set it, the effective variable set, script levels in execution order, every `{{token}}` the request reads with its kind and where it resolves, the unresolved ones, the environment actually in effect, and the effective transport settings. Its `notResolved` field states what it deliberately does not answer. |
| `list_environments` | Environments with variable names, `secret` flags, masked values for secrets, plain values for non-secrets. |
| `list_flows` / `get_flow` | Flow definitions (see schema below). |
| `get_history` | Recent runs of a request: status, duration, redacted headers, response body — lets an agent learn response shapes without re-calling. |
| `describe_usage` | The machine-readable guide: this document's rules, the Flow schema, authoring conventions, worked examples. Agents call it before authoring. |

Run tier (default on):

| Tool | Purpose |
| --- | --- |
| `run_request` | Execute a stored request: `{requestId, environmentId?, variables?}` where `variables` may override non-secret variables only, and may not resolve to a secret (rule 8). Returns status, duration, redacted headers, full body, script test results. |
| `run_flow` | Execute a Flow: `{flowId, environmentId?, inputs?}`. Returns per-step summaries (status, extracted values with secrets masked, assertion results) and the flow's declared outputs. |

`environmentId` names a **collection** environment. Omitting it means no
collection environment applies; it does not fall back to whichever environment
is selected in the app's window, because that selection is frontend state the Go
process cannot read. The workspace's active global environment applies either
way and cannot be selected per call. This matters for more than fidelity: the
selected environment is part of the approval key, so the same request under a
different environment is a different site and re-asks.

Write tier (default off; enabling is a Settings action):

| Tool | Purpose |
| --- | --- |
| `create_request` / `update_request` | Author a request in a collection. Validated against the same model the UI edits. |
| `create_flow` / `update_flow` | Author a Flow. Validated against the schema below; unknown request ids, unknown variables, and cycles are rejected with actionable errors. |

Design conventions for the tools themselves: every id the tools return is
accepted by every tool that takes an id; every error names the field and the
fix; list tools support a `limit` and return stable ordering; nothing ever
returns a resolved secret, including in errors.

## Flows

A Flow is a first-class, exportable entity stored alongside requests in the
collection: an ordered chain of requests with data wiring, assertions, and
declared outputs. The canonical example: query a GraphQL API, take fields from
its response into the payload of a POST to API B, take fields from B's
response, call API C, assert success.

```json
{
  "id": "flow_8f3k",
  "name": "Provision POS terminal",
  "description": "GraphQL lookup -> create terminal on API B -> activate on API C",
  "inputs": [
    { "name": "storeCode", "required": true, "description": "Store short code, e.g. DHK-04" }
  ],
  "steps": [
    {
      "id": "lookup",
      "requestId": "req_graphql_store",
      "vars": { "code": "{{storeCode}}" },
      "extract": [
        { "name": "storeId",  "from": "body", "path": "$.data.store.id" },
        { "name": "region",   "from": "body", "path": "$.data.store.region" }
      ],
      "assert": [ { "type": "status", "equals": 200 } ]
    },
    {
      "id": "createTerminal",
      "requestId": "req_apib_create_terminal",
      "vars": { "storeId": "{{storeId}}", "region": "{{region}}" },
      "extract": [ { "name": "terminalId", "from": "body", "path": "$.terminal.id" } ],
      "assert": [
        { "type": "status", "in": [200, 201] },
        { "type": "body",  "path": "$.terminal.state", "equals": "created" }
      ]
    },
    {
      "id": "activate",
      "requestId": "req_apic_activate",
      "vars": { "terminalId": "{{terminalId}}" },
      "assert": [ { "type": "status", "equals": 200 } ]
    }
  ],
  "outputs": [ { "name": "terminalId", "value": "{{terminalId}}" } ]
}
```

Semantics:

- Steps run in order. `vars` set flow-scoped variables for that step's request
  resolution; they layer over the selected environment (secrets still resolve
  from the environment, and the step's own destination is checked against that
  step's own request definition).
- `extract` pulls values from the step's response into flow scope. `from` is
  `body` (a JSONPath), `header` (header name), or `status`. A failed extraction
  fails the step with the path named.
- **The JSONPath is a deliberately small subset**, and it is the same subset for
  extractions and body assertions: `$` root (a bare `$` is refused — name a
  value inside it), dot property access (`$.data.store.id`), bracketed quoted
  keys for names dots cannot express (`$["key with spaces"]`), and non-negative
  numeric array indexes (`$.items[0].id`). Wildcards (`*`), filter expressions,
  slices, recursive descent (`..`), negative indexes and functions are **not
  supported** and are rejected when the path is parsed, with the offending path
  quoted. That is a design choice rather than an unfinished implementation: a
  flow variable holds exactly one value, and every excluded form names either
  zero values or many, so accepting one would mean inventing a rule ("the first
  match") the author never wrote. A path that parses but resolves to nothing
  fails the step and says how far it got, rather than carrying an empty string
  into the next request. Resolved values render as a string unquoted, a number
  byte-for-byte as the server sent it (no float rounding), and an object or
  array as compact JSON — so `$.filter` is usable as a whole sub-document to
  post onward.
- `assert` supports `status` (`equals`/`in`) and `body` (JSONPath +
  `equals`/`exists`/`contains`). Any failed assertion stops the flow
  (fail-fast), reporting which step and which assertion.
- Each step's request runs with its full normal machinery — pre/post scripts
  included — through the same execution path as a UI Send.
- `outputs` name what the flow hands back to its caller.
- The same flow runs identically from the app's Flow tab and from `run_flow`.

## What shipped

The interface was built in six phases, all of them delivered. The table is
kept because it is the shortest map of where each capability lives.

| Phase | Delivered |
| --- | --- |
| 1 | MCP server skeleton (localhost HTTP + token, Settings toggle), read-tier tools, redaction layer. |
| 2 | `run_request`, audit log + panel, the per-secret new-host guard + approval prompt. |
| 3 | Flow model, runner, Flow tab UI, `run_flow`, `list_flows`/`get_flow`. |
| 4 | Write tier (`create_*`/`update_*`) behind its gate, `describe_usage`. |
| 5 | Headless `liteapi mcp` stdio mode. |
| 6 | Surface reduction and the destination boundary (§1, §2): the per-secret host guard replaced by per-(site, origin, kind) authorization at every egress, non-persistence of agent runs, the four refusal classes, site-scoped approvals, an agent-safe history projection, `inspect_request`. |

Phase 6 replaced Phase 2's guard rather than adding to it, and it is worth
saying why in one place. The old guard learned a host allowlist per secret,
reasoned about the request's definition, and ran once before the send. Three
things follow that it could not do and this one does: it never saw a
pre-request script that rewrote the URL after the check, or a redirect hop; it
compared bare hostnames with the port deliberately dropped, so `:3000` and
`:8080` on one host were one trust decision; and a remembered `(secret, host)`
pair authorized that pair everywhere, across requests and environments. The
attack that opened the phase needed none of that subtlety — a request variable
whose value was `{{apiToken}}` and a header reading `Bearer {{alias}}` named no
secret anywhere, so the guard found nothing to protect and never computed a host
at all. Nothing in the current design detects that alias either. It does not
need to: the boundary never asks which credential is travelling, only whether
this request's own definition points where the send is about to go.

An agent that has never read this document gets the same material from
`describe_usage`, which is assembled from Go data in `internal/mcpserver` and
reports the write tier's live state — so what it says about what is possible is
true of the install it is talking to. It states the same non-guarantees this
document does, in the same words where they matter: an agent that is told the
boundary is stronger than it is will make worse decisions than one told the
truth.
