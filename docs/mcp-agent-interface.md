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

## Non-negotiable safety rules

These rules bind every tool below. They are enforced in the Go process, not by
tool descriptions.

1. **Secrets never cross the MCP boundary.** Request definitions are returned
   with `{{templates}}` unresolved. Secret variables are listed as name +
   `secret: true`, value always masked. Resolution to real values happens only
   inside LiteAPI at send time. No tool, argument, or error message may carry a
   resolved secret value.
2. **Sent-request echoes show templates.** When a tool reports what was sent,
   header/URL/body values that came from secret variables appear as their
   template form, never the resolved bytes.
3. **Response headers are redacted.** `Authorization`, `Proxy-Authorization`,
   `Set-Cookie`, `Cookie`, and headers matching `*api-key*`/`*token*`
   (case-insensitive) are masked in responses shown to the agent. Response
   bodies pass through in full — that is the data the agent is there for.
4. **The new-host guard.** Every secret variable carries a host allowlist
   learned from the requests that already use it. A run (agent-initiated) that
   would resolve a secret into a request aimed at a host outside that allowlist
   blocks and raises an approval prompt in the app UI. The user approves (host
   is added) or denies (the run fails with a clear, secret-free error).
5. **Write tier is off by default.** `create_*`/`update_*` tools are rejected
   until the user enables writes in Settings. Even then, agents can reference
   secret variables by name but can never read or define secret values.
6. **Everything is audited.** Every MCP call is recorded (tool, arguments
   summary, outcome, timestamp) and visible in the app's audit panel.

## Transport and pairing

The server speaks MCP over streamable HTTP on `127.0.0.1:<port>` (default
must not collide with `wails dev`'s 34115) and requires a bearer token
generated per install. It runs only while the toggle in Settings → AI access
is on. Settings shows a copyable one-liner:

```
claude mcp add --transport http liteapi http://127.0.0.1:<port>/mcp --header "Authorization: Bearer <token>"
```

A later phase adds `liteapi mcp` (stdio, headless) over the same store for
machines where the app is not running.

## Tool surface

Read tier (always on while the server is enabled):

| Tool | Purpose |
| --- | --- |
| `list_collections` | Collections with id, name, request/flow counts. |
| `list_requests` | Requests in a collection: id, name, method, URL template, folder path. |
| `search_requests` | Substring/keyword search across names, URLs, folder paths. |
| `get_request` | Full definition (redacted): method, URL, headers, params, body, auth *mode* (never credentials), scripts, settings. |
| `list_environments` | Environments with variable names, `secret` flags, masked values for secrets, plain values for non-secrets. |
| `list_flows` / `get_flow` | Flow definitions (see schema below). |
| `get_history` | Recent runs of a request: status, duration, redacted headers, response body — lets an agent learn response shapes without re-calling. |
| `describe_usage` | The machine-readable guide: this document's rules, the Flow schema, authoring conventions, worked examples. Agents call it before authoring. |

Run tier (default on):

| Tool | Purpose |
| --- | --- |
| `run_request` | Execute a stored request: `{requestId, environmentId?, variables?}` where `variables` may override non-secret variables only. Returns status, duration, redacted headers, full body, script test results. |
| `run_flow` | Execute a Flow: `{flowId, environmentId?, inputs?}`. Returns per-step summaries (status, extracted values with secrets masked, assertion results) and the flow's declared outputs. |

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
  from the environment, subject to the new-host guard).
- `extract` pulls values from the step's response into flow scope. `from` is
  `body` (JSONPath), `header` (header name), or `status`. A failed extraction
  fails the step with the path named.
- `assert` supports `status` (`equals`/`in`) and `body` (JSONPath +
  `equals`/`exists`/`contains`). Any failed assertion stops the flow
  (fail-fast), reporting which step and which assertion.
- Each step's request runs with its full normal machinery — pre/post scripts
  included — through the same execution path as a UI Send.
- `outputs` name what the flow hands back to its caller.
- The same flow runs identically from the app's Flow tab and from `run_flow`.

## Phasing

| Phase | Delivers |
| --- | --- |
| 1 | MCP server skeleton (localhost HTTP + token, Settings toggle), read-tier tools, redaction layer. |
| 2 | `run_request`, audit log + panel, new-host guard + approval prompt. |
| 3 | Flow model, runner, Flow tab UI, `run_flow`, `list_flows`/`get_flow`. |
| 4 | Write tier (`create_*`/`update_*`), `describe_usage`. |
| 5 | Headless `liteapi mcp` stdio mode, docs polish. |
