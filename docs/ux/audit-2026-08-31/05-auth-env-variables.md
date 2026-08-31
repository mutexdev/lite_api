# A5 — Auth, environments, variables, secrets

## Summary

- Selecting `inherit` as an auth mode shows a message written for unimplemented signers ("marked partial until its full backend signer is implemented") — actively misleading, since `inherit` is the only mode that can reach that fallback branch.
- The "secret" checkbox in the Environment, Global-environment, and Collection-variable tables is decorative: the value `<input>` is never switched to `type="password"` there, so exactly the screens most likely to hold real secrets show them in plain text.
- Folder-level Auth is a materially crippled copy of Request/Collection-level Auth — OAuth2 loses ~14 fields, OAuth1 loses ~9, AWSv4 loses 2 — with no indication anything is missing.
- The same "API key placement" concept persists a different value (`query` vs `queryparams`) depending on whether you set it at request/collection level or folder level.
- The app uses three unrelated widget idioms (native `<select>`, `.tabs`, `.segmented`) for the same "switch between 2–4 modes" interaction across Body mode, Response view, and the `.env` Table/Raw toggle.
- OAuth2 — the most complex form in the app — never shows a token, its expiry, or a refresh/error state anywhere in the Auth tab; the only OAuth2 UI beyond the config form is a modal that appears reactively during Send.
- The `{{variable}}` chip has three independent visual implementations (URL bar/tables, the "variable inspector" strip, the CodeMirror editor) with three different border-radius values, two different "valid" background tokens, and a secret/invalid treatment that differs by surface.
- The unresolved-variable warning — built specifically to stop silent `{{missing}}` text going out on the wire — is wired to headers only; the same failure mode is unguarded in params, the body, and every Auth field.
- Secret reveal has two postures: a `{{var}}` reference gets a Show/Hide toggle in its tooltip; a literal secret typed into any field (Bearer token, Client secret, API key value…) is a bare masked input with no reveal affordance at all.

## Findings

### A5-01 — "Inherit" auth mode shows an "unimplemented" warning
- **Severity**: critical
- **Where**: `frontend/src/App.svelte:1135` (auth mode list), `frontend/src/App.svelte:9738-9739`
- **What the user sees**: Selecting `inherit` from the Auth "Mode" dropdown renders: *"This auth mode is marked partial until its full backend signer is implemented."*
- **Why it's wrong**: `authModes` is `['none', 'inherit', 'basic', 'bearer', 'apikey', 'oauth2', 'awsv4', 'digest', 'ntlm', 'oauth1', 'wsse']`. Every mode except `none` and `inherit` is handled by its own `{:else if}` branch (basic/digest/wsse/ntlm at 9576, bearer at 9585, oauth2 at 9588, apikey at 9673, awsv4 at 9683, oauth1 at 9696). The final catch-all, `{:else if activeRequest.auth.mode !== 'none'}` at 9738, is therefore reachable **only** by `inherit`. `inherit` is a legitimate, common concept (use the parent folder/collection's auth) that correctly has no fields of its own — but the message tells the user their choice is an unfinished feature, which is false and will make people avoid a working, load-bearing mode.
- **Proposed fix**: Give `inherit` its own branch with a real explanation, e.g. "Uses the auth configured on the parent folder/collection." Reserve the "partial" message (if still needed for any mode) for an actual unimplemented signer, and never let unrelated modes fall through to it.
- **Shared primitive it should use**: A generic `<AuthModeEmptyState mode>` that maps each non-field mode to its own copy, rather than one catch-all string.

### A5-02 — "Secret" checkbox does not mask values in Environment/Collection variable tables
- **Severity**: critical
- **Where**: `frontend/src/App.svelte:11291` (Environment variables), `frontend/src/App.svelte:11230` (Global environment variables), `frontend/src/App.svelte:11381` (Collection variables) — contrast with `frontend/src/lib/KeyValueTable.svelte:430-431`
- **What the user sees**: In the Environments panel, checking "Secret" on a row (`<input aria-label="Environment variable secret" type="checkbox" ...>` at 11300, similarly 11239 and 11382) has no visible effect — the adjacent Value `<input>` (11291: `<input aria-label="Environment variable value" value={String(row.variable.value ?? '')} oninput=... />`) is never given `type="password"`. The value stays in plain text whether or not "Secret" is checked.
- **Why it's wrong**: `lib/KeyValueTable.svelte:431` already implements the correct pattern used elsewhere in the app — `type={row.secret ? 'password' : 'text'}` — but the Environment, Global-environment, and Collection-variable editors are hand-rolled `<table>` markup that never adopted it. This is exactly the screen where a user pastes a real API key or token, on a screen-share or a screenshot, believing the checkbox they just ticked protects it.
- **Proposed fix**: Route all three tables through `KeyValueTable` (or extract its masked-value-input logic into a shared primitive) so `secret` always masks the value, everywhere it appears as a field.
- **Shared primitive it should use**: One masked-value input component (see "The variable visual language" / secrets section below), used by every table that has a `secret` column.

### A5-03 — Folder-level Auth is a stripped-down copy of Request/Collection Auth
- **Severity**: major
- **Where**: `frontend/src/App.svelte:10463-10536` (folder), vs. `frontend/src/App.svelte:9568-9741` (request) and `frontend/src/App.svelte:10581-10743` (collection)
- **What the user sees**: Setting OAuth2 auth on a folder offers only Grant type, Access token URL, Client ID, Client secret, Scope, and a "Token" field (10518-10534). Setting it on a request or collection offers all of that plus Callback URL, Authorization URL, username/password (password grant), State, Credentials placement, PKCE, Token source, Token placement, Header prefix/Query key, and three `OAuth2AdditionalParams` blocks (authorization/token/refresh). Folder-level OAuth1 (10503-10517) keeps only Consumer key/secret, Access token, Token secret, Signature — losing Placement, Callback URL, Verifier, Timestamp, Nonce, Version, Realm, Private key (+type), and Body hash, all present at request/collection level (9696-9737). Folder AWSv4 (10494-10502) drops Session token and Profile.
- **Why it's wrong**: A user who configures OAuth2 with PKCE + a custom token placement on a request, then decides to hoist that config to the folder so every request under it inherits it, silently loses most of the configuration with no warning that folder-level auth is a different, smaller feature.
- **Proposed fix**: Back all four auth surfaces (request, folder, collection, workspace-default) with one schema-driven `AuthForm` component so the field set for a given mode is identical everywhere it can be configured.
- **Shared primitive it should use**: A single `AuthForm.svelte` taking `{ mode, value, onChange }`, parameterized only by which levels are read-only (e.g., folder auth has no per-request PKCE reason to differ — if a field is meaningful anywhere it should be meaningful everywhere).

### A5-04 — API key "placement" persists a different value depending on level
- **Severity**: major
- **Where**: `frontend/src/App.svelte:9679-9682` (request), `frontend/src/App.svelte:10692-10695` (collection) vs. `frontend/src/App.svelte:10490-10493` (folder)
- **What the user sees**: Request and collection-level API key auth offer a "Send in" select with `<option value="header">Header</option><option value="query">Query</option>`. Folder-level offers a "Placement" select with `<option value="header">Header</option><option value="queryparams">Query params</option>` — different label ("Placement" vs "Send in"), different option text ("Query params" vs "Query"), and critically a **different persisted value** (`queryparams` vs `query`) for the same semantic choice.
- **Why it's wrong**: If the backend's auth signer expects one canonical value for "send in query string," a folder-level config using `queryparams` may not be recognized the same way a request-level config using `query` is — this looks like a functional bug riding on a labeling inconsistency, not just a cosmetic one.
- **Proposed fix**: Use one field name, one label ("Send in"), one option set (`header` / `query`) everywhere API key placement is configured.
- **Shared primitive it should use**: Same `AuthForm` component as A5-03 — this divergence exists because folder auth is a hand-copied second implementation.

### A5-05 — Three different widgets for "switch between N display modes"
- **Severity**: major
- **Where**: `frontend/src/App.svelte:9511-9515` (Body mode), `frontend/src/lib/workbench/ResponseInspector.svelte:357` (Response view), `frontend/src/App.svelte:11331-11334` (`.env` Table/Raw), `frontend/src/App.svelte:10089` (JavaScript sandbox mode)
- **What the user sees**: Body mode is a native `<select>`. Response Pretty/Raw/Base64/Hex is also a native `<select>` (`<select aria-label="Response view" data-testid="response-view-select" ...>`). The `.env` file's Table/Raw toggle is a pair of buttons in a `<div class="tabs compact">`. The JS sandbox mode picker is `<div class="segmented compact" role="radiogroup" aria-label="JavaScript sandbox mode">`. Three visually and interactively distinct control families for the same underlying gesture: pick one of a small fixed set of views.
- **Why it's wrong**: `.tabs` and `.segmented` are genuinely different CSS constructs (`style.css:1584/1630` vs `style.css:4306`), so this isn't a labeling nit — a user learns to recognize "the pill row" as a mode switch in one place and then has to re-learn it as a dropdown in another, for functionally identical interactions. This is a direct instance of "the app looks like a different application in each section."
- **Proposed fix**: Standardize on one control — a segmented control reads best for 2-4 fixed options with no scrolling — and use it for Body mode, Response view, and `.env` Table/Raw alike. Reserve `<select>` for option sets that can grow unbounded (e.g., auth Mode, which has 11 options).
- **Shared primitive it should use**: The existing `.segmented` component/class, generalized into a `<ModeSwitch options onChange>` component.

### A5-06 — No OAuth2 token/expiry/refresh/error visibility anywhere in the Auth tab
- **Severity**: major
- **Where**: `frontend/src/App.svelte:9588-9672` (OAuth2 fields), `frontend/src/App.svelte:1918-1923` (the only trigger for token UI), `frontend/src/lib/modals/confirm/OAuth2AuthorizationModal.svelte`
- **What the user sees**: The OAuth2 config form (grant type through the three `OAuth2AdditionalParams` stacks) has no field, badge, or button that shows whether a token has been fetched, when it expires, whether it was refreshed, or whether the last fetch failed. The only OAuth2-flow UI in the app is `OAuth2AuthorizationModal`, which appears only because the backend emits an `oauth2:authorize` event (`stopOAuth2Authorize = EventsOn('oauth2:authorize', ...)` at 1918) — in practice, only when the user clicks Send and the backend decides interactive auth is needed. There is no "Get new access token" button a user can press proactively, and no persisted-token status once one exists.
- **Why it's wrong**: OAuth2 is explicitly called out as the most complex form in the app, and Postman/Insomnia's core OAuth2 UX is "configure → click Get New Access Token → see the token, its expiry, and a way to clear/refresh it." Here the entire lifecycle is invisible until a request happens to trigger the modal. The "Static token" field (9648-9649, `<span class="field-label">Static token</span><input type="password" value={activeRequest.auth.token} ... />`) compounds the confusion: it binds to the exact same `activeRequest.auth.token` field Bearer mode uses, with no label or help text explaining when this manual value is used versus an auto-fetched one.
- **Proposed fix**: Add a token-status row to the OAuth2 form: state (No token / Token active, expires in Xm / Expired / Refresh failed: <error>), a manual "Get new access token" button, and a "Clear token" action. Rename or annotate "Static token" to make its relationship to auto-fetched tokens explicit.
- **Shared primitive it should use**: A small `OAuth2TokenStatus` component reusable at request, folder, and collection levels (wherever OAuth2 can be configured).

### A5-07 — Three independent visual implementations of the `{{variable}}` chip
- **Severity**: major
- **Where**: `frontend/src/lib/VariableTextOverlay.svelte:99-103` + `frontend/src/style.css:2112-2129` (URL bar & KeyValueTable overlay); `frontend/src/App.svelte:9260-9263` + `frontend/src/style.css:2373-2391` (the "variable inspector" chip strip below headers); `frontend/src/lib/workbench/CodeEditor.svelte:159-162` (CodeMirror body/script/test/docs editors)
- **What the user sees**: A resolved variable is a bold, borderless, `var(--radius-3)` pill with `background: var(--accent-tint)` in the URL bar and tables. The same concept in the "variable inspector" strip is `.variable-chip`: a bordered (`1px solid var(--accent-border)`) `var(--radius-6)` pill with `background: var(--accent-soft)` — a genuinely different color (`--accent-tint: rgba(255,108,55,0.09)` vs `--accent-soft: #fff0eb` in the light theme, `style.css:69-71`). Inside the JSON/XML/script/test/docs CodeMirror editors, `CodeEditor.svelte` defines its own inline `baseTheme`: `.cm-variable { borderRadius: '2px' }` (a hardcoded pixel value, not any `--radius-*` token), `.cm-variable-valid { backgroundColor: 'var(--accent-soft)' }` with no color or font-weight override (so it isn't bold like the other two), `.cm-variable-missing`/`.cm-variable-invalid` add a `wavy underline` in `var(--danger)` that appears nowhere else in the app, and `.cm-variable-secret { borderBottom: '1px dotted var(--warning-strong)' }` — a secret-state treatment that exists only in this one renderer.
- **Why it's wrong**: This is the single most direct evidence for "the app looks like a different application in each section" as it applies to variables. A user who learns "orange pill = resolved variable" in the URL bar sees a differently-shaded, differently-shaped pill in the header inspector, and a third, unbolded, sometimes-wavy-underlined treatment in the body/script editor.
- **Proposed fix**: See "The variable visual language" section below for the exact merged spec.
- **Shared primitive it should use**: One `VariableChip` visual spec (CSS custom properties + class names) consumed by both the CodeMirror decoration extension and the plain-text overlay component, instead of two hand-maintained style definitions that happen to reuse some class names by coincidence.

### A5-08 — Secret reveal affordance differs for variable references vs. literal secret text
- **Severity**: major
- **Where**: `frontend/src/lib/VariableTextOverlay.svelte:142-146` (Show/Hide button) vs. `frontend/src/lib/KeyValueTable.svelte:430-431`, `frontend/src/App.svelte:9580` (basic password), `9587` (bearer token), `9606` (oauth2 client secret), `9649` (static token), `9677` (apikey value), `9687-9689` (awsv4 secret/session token), `9700`/`9704` (oauth1 consumer/token secret)
- **What the user sees**: A `{{secretVar}}` reference anywhere gets a tooltip with an explicit `Show`/`Hide` button (`variableTooltips.toggleRevealed`). A literal secret value typed directly into any Auth field, or into a KeyValueTable row marked secret, is a bare `type="password"` input — no eye icon, no click-to-reveal, no way to check what you typed short of retyping it or copying it out.
- **Why it's wrong**: These are the same underlying concept (a value the user doesn't want shoulder-surfed) with two different levels of user control depending on whether the value happens to be a variable reference or literal text. Most Auth-tab secrets (Client Secret, Consumer Secret, AWS Secret Key) are literal text, i.e. the worse-served case.
- **Proposed fix**: Give every masked input the same reveal affordance — an eye-icon toggle button next to the field — regardless of whether its content is a literal or a `{{variable}}`.
- **Shared primitive it should use**: The shared masked-value input from A5-02, with a built-in reveal toggle used everywhere `type="password"` currently appears bare.

### A5-09 — Unresolved-variable warning only checks headers
- **Severity**: major
- **Where**: `frontend/src/lib/unresolvedVariables.ts:66-81` (`unresolvedHeaderVariables`, headers-only by name), `frontend/src/App.svelte:1675-1686` (wired only to `request.headers`), `frontend/src/App.svelte:9254-9255` (the only warning banner in the app)
- **What the user sees**: If a header value references `{{missingVar}}`, a banner appears: *"Unresolved variable in headers: {{missingVar}} — sent as literal text."* The same mistake in a query param value, the request body, the OAuth2 Client ID/Secret/Scope fields, an API Key value, or an AWS access key produces no warning at all — the request goes out with literal `{{...}}` text and the user is left debugging a 401/400 with no clue.
- **Why it's wrong**: The code comment at `App.svelte:9241-9247` explicitly frames this feature as fixing "the one place an unresolved `{{variable}}` was completely silent." It solved that for headers only; every other interpolated surface — most notably every Auth field, which is squarely this audit's territory — still has the original silent-failure bug this feature exists to prevent.
- **Proposed fix**: Extend `unresolvedVariables.ts` with `unresolvedFieldVariables` (or generalize `unresolvedHeaderVariables`) to scan params, the URL, the body (where it's plain text), and the active Auth mode's fields, and merge all of it into one warning banner listing every unresolved reference and where it was found.
- **Shared primitive it should use**: The existing `resolves()` predicate and `unresolvedVariableMessage()` formatter already generalize cleanly — this is a wiring gap, not a missing capability.

### A5-10 — `.env` editor has no secret concept at all
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:11358-11359`
- **What the user sees**: The `.env` file Table view renders `<input aria-label=".env variable name" .../>` and `<input aria-label=".env variable value" .../>` with no secret checkbox column and no masking — unlike the Environment/Global-environment/Collection-variable tables immediately adjacent in the same panel, which at least have a (currently non-functional per A5-02) "Secret" checkbox.
- **Why it's wrong**: `.env` files are exactly where users paste raw API keys and tokens. This is a third posture on the same underlying concept within one settings panel: Collection/Environment tables have a secret checkbox that does nothing visually (A5-02); `.env` doesn't even have the checkbox.
- **Proposed fix**: Once A5-02 is fixed, extend the same masked-value affordance to `.env` rows, or at minimum add the checkbox so the concept isn't entirely absent from this surface.
- **Shared primitive it should use**: Same masked-value input as A5-02/A5-08.

### A5-11 — Auth form width is capped in some places, unbounded in others
- **Severity**: minor
- **Where**: `frontend/src/style.css:5106-5108` (`.auth-grid { max-width: 680px; }`), applied at `frontend/src/App.svelte:10464` (folder) and `frontend/src/App.svelte:10582` (collection modal), but **not** at `frontend/src/App.svelte:9569` (request Auth tab, plain `.field-grid`) or `frontend/src/App.svelte:11400` (workspace/preferences Collection Auth view, also plain `.field-grid`)
- **What the user sees**: The identical Auth form (same fields, same labels) is capped to a comfortable 680px measure in two of the four places it appears, and stretches to the full available width in the other two, changing the label-to-input ratio and line length depending on which screen you're in.
- **Proposed fix**: Apply `.auth-grid`'s width cap everywhere `.field-grid` is used for an Auth form.
- **Shared primitive it should use**: Once A5-03's shared `AuthForm` component exists, this becomes automatic.

### A5-12 — Environment panel border-radius bypasses the token scale
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/EnvironmentContextMenu.svelte:152` (`border-radius: 8px;`)
- **What the user sees**: Nothing directly visible, but the environment dropdown panel's corner radius (8px, hardcoded) doesn't match any defined token — the nearest is `--radius-6` (6px, `style.css:41`), used by the variable tooltip panels this component sits next to conceptually.
- **Proposed fix**: Replace `8px` with `var(--radius-6)` or introduce/use an `--radius-8` token if 8px is intentionally the "popover" radius elsewhere.
- **Shared primitive it should use**: The `--radius-*` token scale already defined in `style.css:38-41`.

### A5-13 — No required-field markers, inconsistent placeholders, no help text across all 11 auth modes
- **Severity**: polish
- **Where**: `frontend/src/App.svelte:9568-9741`
- **What the user sees**: Across every auth mode, fields are plain `<span class="field-label">Label</span><input .../>` pairs with no asterisk/required indicator and no inline help. Placeholder text is sparse and inconsistent: AWSv4 Service/Region (9691, 9693) get example values (`execute-api`, `us-east-1`), OAuth1 Version (9726) gets `1.0`, but Client ID, Client Secret, Access Token URL, Authorization URL, Callback URL, Realm, Nonce, Consumer Key/Secret, and every other text field in the form get none.
- **Why it's wrong**: For a form this dense (OAuth2 alone has ~20 conditionally-shown fields), the complete absence of "what does a valid value look like" guidance and "which of these are required for this grant type" makes the form intimidating to fill out correctly on a first pass.
- **Proposed fix**: Add representative placeholders to every URL/ID/secret field, and mark fields that are required for the current grant type/mode combination.
- **Shared primitive it should use**: The `AuthForm` schema from A5-03 is the natural place to declare `placeholder` and `required` per field/mode once, instead of writing them ad hoc four times over.

### A5-14 — Environment selector trigger hides which global environment is active
- **Severity**: polish
- **Where**: `frontend/src/lib/workbench/EnvironmentContextMenu.svelte:82-90`
- **What the user sees**: The topbar trigger button shows only the collection-scoped environment name (`<span>{environmentName}</span>`, line 89). The active global environment name is available only in the `title` attribute (line 83, a hover tooltip) and inside the dropdown panel. If no collection environment is selected, the button reads "No environment" even while a global environment is actively supplying variables.
- **Why it's wrong**: This is exactly the kind of "which scope am I in" ambiguity that matters most for variables/secrets debugging — the audit's core subject.
- **Proposed fix**: Show both scopes in the trigger when a global environment is active, e.g. `Global: Production · No environment`, or a small dot/badge indicating "global active."
- **Shared primitive it should use**: N/A — localized fix to this component.

## Cross-cutting primitives this area needs

1. **One `AuthForm` component**, schema-driven by mode, shared across request/folder/collection/workspace-default levels (fixes A5-01, A5-03, A5-04, A5-06, A5-11, A5-13). Currently there are effectively four hand-copied implementations of the same form that have already drifted.
2. **One masked-value input primitive** — `type="password"` + eye-icon reveal toggle + copy — used by every field or table column that can hold a secret: KeyValueTable rows, `.env` rows, Environment/Global/Collection variable tables, and every Auth secret field (fixes A5-02, A5-08, A5-10).
3. **One `VariableChip` visual spec**, consumed identically by the CodeMirror decoration extension and the plain-text overlay (fixes A5-07) — see next section for the exact spec.
4. **One `ModeSwitch` segmented-control component** for all fixed-small-option-set toggles (Body mode, Response view, `.env` Table/Raw), reserving native `<select>` for open-ended lists like Auth Mode (fixes A5-05).
5. **A generalized unresolved-variable scanner** covering params, URL, body, and Auth fields, not just headers (fixes A5-09).
6. **An `OAuth2TokenStatus` component** showing token presence/expiry/refresh/error with a manual "Get new access token" action, attached to the Auth tab at every level OAuth2 can be configured (fixes A5-06).

## The variable visual language

One `{{variable}}` treatment, expressed as a single component/class set, for every surface: URL bar, KeyValueTable rows (including secret rows, which currently get no chip at all), the CodeMirror body/script/test/docs editors, the "variable inspector" strip, and — once masked values are wired up — Environment/`.env` raw inputs that contain a reference.

**Shape (all states)**: inline pill, code font, `font-weight: 700`, `border-radius: var(--radius-4)` (pick one token — currently 3px/6px/2px compete; 4px splits the difference and becomes the one answer), `1px solid` border always present (today only the "inspector" chip has a border — the others should gain one so the chip reads consistently even in dense text or on unfamiliar backgrounds).

| State | Background | Text / border color | Extra marker |
|---|---|---|---|
| **Resolved, not secret** | `var(--accent-tint)` | `var(--accent)` / `var(--accent-border)` | none |
| **Resolved, secret** | `var(--accent-tint)` | `var(--accent)` / `var(--accent-border)` | content is always `••••` regardless of surface (never briefly render the plaintext in the pill itself); a small lock glyph prefixed to the token text |
| **Missing** (valid name, not found in any scope) | `var(--warning-bg-soft)` | `var(--warning-strong)` / dashed `var(--warning-strong)` border | — deliberately warning-colored, not danger-colored, so it reads as "not yet defined" rather than "broken," and is visually distinct from Invalid |
| **Invalid name** (fails `isValidVariableName`) | `var(--danger-tint)` | `var(--danger-strong)` / `var(--danger-border)` | — reserve solid danger only for this and the send-time warning banner |
| **Prompt** (`{{?name}}`) | `var(--info-tint)` (or nearest existing info token) | `var(--info)` | leading `?` glyph |

**Interaction (all states)**: click/Enter/Space opens one shared tooltip component — the exact markup already shared by `VariableTextOverlay.svelte:104-148` and the App.svelte inspector chip at 9264-9298 should become the single implementation both consume. Tooltip always shows: name, scope badge, current value (masked + Show/Hide for secrets, using the shared reveal toggle from primitive #2), Copy button, and a read-only note when applicable. No surface gets a lesser version — the CodeMirror editors currently offer only a native browser tooltip via a `title` attribute and a hardcoded "secret value hidden" with no reveal option; they should render the same rich tooltip on click, not a hover-only OS tooltip.

**Implementation note**: today the CodeMirror surfaces (`CodeEditor.svelte:159-162`) define colors via a JS `baseTheme` object independent of the class names used elsewhere, which is how `--accent-soft` (editor) and `--accent-tint` (everywhere else) ended up as two different "valid" colors despite both meaning the same thing. The fix is mechanical once the token choice above is fixed: point the CodeMirror `baseTheme` at the exact same CSS custom properties and drop the one-off wavy-underline/dotted-underline treatments in favor of the shared background+border+lock-glyph vocabulary.
