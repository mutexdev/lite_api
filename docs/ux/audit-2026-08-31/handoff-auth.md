# Handoff — A5, auth / environments / variables / secrets

Pass X6, sole owner of `frontend/src/App.svelte` for this wave.
`05-auth-env-variables.md` is the spec; A5-01 and A5-02 were already closed
before this pass started.

## Verification

From `frontend/`, after the last edit:

| Gate | Result |
| --- | --- |
| `npm run check` | 287 files, **0 errors, 0 warnings** |
| `npm test` | **1334 tests, 1334 pass, 0 fail** |
| `npm run lint` | clean |

One run in the middle of the final sequence reported a single failure that no
subsequent run reproduced — a source-text test reading a `.svelte` file while
another agent was writing it. Two consecutive clean 1334/1334 runs followed.

Two further failures were seen earlier in the pass and were **not** mine — both in the preferences
implementer's in-flight files, and both gone by the final run:
`test/preferencesRows.test.mts` looking for `views/PreferencesPanel.svelte`
while the file was at `views/preferences/PreferencesPanel.svelte`, and a
`store_rune_conflict` in `views/preferences/CacheSection.svelte`.

`App.svelte` went from 12,551 to 12,057 lines — 494 net, against ~555 lines of
auth markup deleted and the new folder additional-param handlers, `.env`
masking and comments added back. Two passes are queued behind
this one; the file compiles and every `data-testid` in it is unchanged (the diff
adds none and removes none).

## What closed, and what did not

| Finding | State | Where |
| --- | --- | --- |
| A5-03 four hand-copied auth forms | **closed** | `lib/authFields.ts`, `lib/AuthForm.svelte` |
| A5-04 API key placement writes two values | **closed, incl. migration** | `authFields.ts`, `App.svelte:folderAuthWithDefaults` |
| A5-05 three widgets for one gesture | **closed in my region** (`.env` toggle); two surfaces are not mine | below |
| A5-06 no OAuth2 token visibility | **partially closed** — needs one Go binding | `lib/oauth2TokenStatus.ts` |
| A5-07 three variable chips | **closed for the two DOM surfaces**; CodeMirror needs the block below | `lib/VariableChip.svelte`, `lib/VariableTooltip.svelte` |
| A5-08 secret reveal parity | **closed for auth fields and `.env`**; the six variable tables are pinned by `secretMasking` and left alone | `lib/SecretInput.svelte` |
| A5-09 warning covers headers only | **closed** | `lib/unresolvedVariables.ts` |
| A5-10 `.env` has no secret concept | **closed** (view-level masking) | `App.svelte` |
| A5-11 auth form width | **closed** | `AuthForm.svelte` `<style>` |
| A5-12 environment panel radius | **not mine** — `lib/workbench/EnvironmentContextMenu.svelte` | below |
| A5-13 required markers, placeholders, help | **closed** | `authFields.ts` |
| A5-14 global environment hidden in trigger | **not mine** — `EnvironmentContextMenu.svelte` | below |

## Changes per file

### New — `frontend/src/lib/authFields.ts`
The auth field set as data: `AuthField` descriptors carrying label, kind
(`text`/`secret`/`select`/`checkbox`/`textarea`), `required`, `placeholder`,
`help`, option lists and protocol fallbacks. `authFieldsFor(mode, grantType)`
plus `oauth2TokenPlacementField(tokenPlacement)` for the one field that depends
on a second stored value. `apiKeyPlacementOptions` is the single vocabulary for
A5-04; `normalizeApiKeyPlacement()` reads the legacy folder spelling.
`authModeNotes` holds the `none`/`inherit` copy.

Three fields exist in `types.OAuth2Auth` and were reachable from no form at any
level: `refreshTokenUrl`, `autoFetchToken`, `autoRefreshToken`. `refreshTokenUrl`
is now in the schema; the two booleans are deliberately still out — see "left
undone".

### New — `frontend/src/lib/AuthForm.svelte`
Renders the schema. Props are the four update callbacks, `modeLabel`,
`allowUnset` (folder only), the three OAuth2 additional-param callbacks, and
`tokenRecord`/`onFetchToken` for the token panel. Owns the 680px measure
(A5-11), the required marker with an `.sr-only` twin, per-field help text, and
the OAuth2 token-status row.

### New — `frontend/src/lib/SecretInput.svelte`
`type="password"` plus a Show/Hide button. Deliberately the same two words the
variable tooltip already uses rather than an eye glyph — `Icon.svelte` has no
eye and, more to the point, a second wordless idiom for the same gesture would
have been a fourth widget where the audit found three. Reveal is `$state` and
never persisted.

### New — `frontend/src/lib/variableChipState.ts`, `lib/VariableChip.svelte`
The five states (resolved / secret / missing / invalid / prompt) and the one
pill that paints them. `--radius-4`, always a 1px border, code font, 700 weight.
Missing is amber and dashed; only invalid is red.

### New — `frontend/src/lib/VariableTooltip.svelte`
The panel, extracted from its three copies (App.svelte URL bar, App.svelte
inspector strip, `VariableTextOverlay`). `panelClass` is a prop because the two
families are still positioned by different global rules — see the style.css
block below, which collapses that too.

### New — `frontend/src/lib/oauth2TokenStatus.ts`
Pure token-status derivation. Takes `(config, record, now)`; `record` is
`undefined` at every call site today because nothing on the Go side exports the
token store, and the `unknown` state exists so the form says *"Not visible
here"* rather than the false *"No token"*.

### Changed — `frontend/src/lib/unresolvedVariables.ts`
Added `unresolvedParamVariables`, `unresolvedBodyVariables`,
`unresolvedAuthVariables` and `unresolvedRequestVariables`. Auth fields are
scanned through `authFields.ts`, so a field added to the form cannot be
forgotten by the scanner. `unresolvedVariableMessage` now derives the place
names from the entries ("in headers", "in the URL and auth") instead of
hard-coding "in headers".

### Changed — `frontend/src/lib/VariableTextOverlay.svelte`
Uses `VariableChip` + `VariableTooltip`. Its local structural copy of
`VariableTooltipInfo` — a fourth definition of the same shape, already one field
behind — was deleted in favour of the real type from `variableResolution.ts`.

### Changed — `frontend/src/App.svelte`
* Four auth blocks (request / folder / collection tab / collection settings)
  replaced by `<AuthForm>`. ~555 lines removed.
* `updateFolderOAuth2AdditionalParam` / `add…` / `remove…` added, so folder
  level finally has the three additional-param buckets.
* `folderAuthWithDefaults` normalises `apiLocation`, migrating `queryparams` →
  `query` the next time anything on that folder's auth is saved.
* `unresolvedHeaderWarning` → `unresolvedRequestWarning`, scanning the whole
  request.
* URL-bar and inspector chips/tooltips replaced by the components.
* `.env` Table/Raw `.tabs compact` → `SegmentedControl`.
* `.env` table gained a "Mask values" switch routed through `SecretInput`.

### New tests
`test/authFields.test.mts` (13), `test/oauth2TokenStatus.test.mts` (11),
`test/variableChip.test.mts` (8), `test/secretReveal.test.mts` (5), plus 12 new
cases in `test/unresolvedVariables.test.mts`.

The anti-drift guards worth knowing about:
* `authFields.test.mts` fails if App.svelte has fewer than four `<AuthForm>`
  call sites, or if it starts spelling `Client secret` / `Consumer secret` /
  `queryparams` out by hand again.
* `variableChip.test.mts` fails if a literal radius or `--accent-soft` returns
  to the chip, or if either surface hand-rolls a chip or tooltip again.
* `secretReveal.test.mts` pins the two remaining bare `type="password"` inputs
  by name, so a new one fails rather than passing unnoticed.

## Paste-ready `style.css` edits

**These are for the style.css owner.** Everything below currently works because
`VariableChip.svelte`'s scoped block out-specifies the global rules; the point
of moving it is that the CodeMirror surface cannot reach a scoped block.

### 1. One missing token, so the prompt chip stops being the odd one out

`--info` exists in `:root` and in the dark theme; there is no `--info-tint` to
pair with `--accent-tint` / `--danger-tint` / `--warning-bg-soft`, which is why
the prompt chip has a dotted border and no fill. Add beside the other tints
(light `:root` around line 75, dark around 214):

```css
/* :root */
--info-tint: rgba(9, 123, 237, 0.09);
```
```css
/* the dark theme block */
--info-tint: rgba(116, 174, 246, 0.14);
```

Then in `VariableChip.svelte`, `[data-state='prompt']` becomes
`background: var(--info-tint); border-style: solid;`.

### 2. Replace the three chip rule sets with one

Delete `style.css:2220-2251` — `.cm-variable-valid`, `.cm-variable-invalid`,
`.cm-variable-prompt` and their `:focus-visible` — and `style.css:2481-2506`
(`.variable-chip`, `.variable-chip-wrapper.invalid .variable-chip`,
`.var-token`). `.cm-variable-prompt` has **no DOM user left at all** as of this
pass; it is dead either way.

Paste in their place, and delete the matching `<style>` block from
`lib/VariableChip.svelte` in the same commit:

```css
/*
  One {{variable}} chip, for the plain-text overlays, the inspector strip and
  the CodeMirror decoration alike. See lib/variableChipState.ts for why missing
  is amber and only invalid is red.
*/
.variable-chip-pill,
.cm-variable {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  max-width: 100%;
  padding: var(--space-1) var(--space-5);
  border: 1px solid transparent;
  border-radius: var(--radius-4);
  background: none;
  font-family: var(--code-font-family);
  font-size: inherit;
  font-weight: 700;
  line-height: inherit;
  cursor: pointer;
  outline: none;
}

.variable-chip-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.variable-chip-glyph { font-size: var(--font-size-11); line-height: 1; }
span.variable-chip-pill { cursor: inherit; }

.variable-chip-pill[data-state='resolved'],
.variable-chip-pill[data-state='secret'],
.cm-variable-valid {
  border-color: var(--accent-border);
  background: var(--accent-tint);
  color: var(--accent);
}

.variable-chip-pill[data-state='missing'],
.cm-variable-missing {
  border-color: var(--warning-strong);
  border-style: dashed;
  background: var(--warning-bg-soft);
  color: var(--warning-strong);
}

.variable-chip-pill[data-state='invalid'],
.cm-variable-invalid {
  border-color: var(--danger-border);
  background: var(--danger-tint);
  color: var(--danger-strong);
}

.variable-chip-pill[data-state='prompt'] {
  border-color: var(--info);
  background: var(--info-tint);
  color: var(--info);
}

.variable-chip-pill:focus-visible {
  box-shadow: 0 0 0 2px var(--focus-ring-strong);
}
```

### 3. One tooltip panel rule

`.variable-tooltip` (2506-2534) and `.CodeMirror-brunoVarInfo.inline-var-tooltip`
(2251-2290) are the same panel with two names and slightly different offsets
(`calc(100% + 7px)` vs `calc(100% + 8px)`). `VariableTooltip.svelte` takes the
class as a prop only because of this. Collapse them onto one selector list at
`calc(100% + 8px)` and the prop can be deleted.

### 4. Optional — the two spacing rules the new markup wants

Not required (both surfaces render correctly without them), but they belong in
style.css rather than in a component:

```css
.secret-field { display: flex; align-items: center; gap: var(--space-6); min-width: 0; }
.secret-field input { flex: 1 1 auto; min-width: 0; }
.secret-field .secret-toggle-button { flex: none; }
```

## What `CodeEditor.svelte` needs

`lib/workbench/CodeEditor.svelte` is not mine. Two changes, both mechanical.

**1. Drop the one-off variable theme.** Delete these four lines from `baseTheme`
(currently 310-313):

```js
'.cm-variable': { borderRadius: '2px' },
'.cm-variable-valid': { backgroundColor: 'var(--accent-soft)' },
'.cm-variable-missing, .cm-variable-invalid': { backgroundColor: 'var(--danger-bg-soft)', textDecoration: 'wavy underline var(--danger)' },
'.cm-variable-secret': { borderBottom: '1px dotted var(--warning-strong)' },
```

They are the source of every divergence the audit measured: the hardcoded 2px
(no token), `--accent-soft` where every other surface uses `--accent-tint`, a
wavy red underline that appears nowhere else in the app, and a dotted-underline
secret treatment that exists only here. With the block above in style.css the
decoration is styled correctly with no theme entry at all.

Note that `.cm-variable-valid` and `.cm-variable-invalid` are **already** being
styled by both style.css and this `baseTheme` today — that overlap is why the
editor chip has a 3px radius from one and an accent-soft fill from the other.

**2. Add the secret and prompt states to the decoration.** At line 428 the
class is built as ``cm-variable cm-variable-${state}`` with `state` ∈
`valid | missing | invalid`, and `cm-variable-secret` appended separately.
To join the five-state vocabulary, emit `cm-variable-resolved` /
`cm-variable-secret` in place of `cm-variable-valid` when `info.secret`, and add
a `prompt` branch for `{{?name}}` (the pattern is `promptVariableTextPattern` in
`lib/urlSegments.ts`). `lib/variableChipState.ts` exports
`variableChipState(info, prompt)` and `variableChipLabel(name, state, scope)` —
use both, and the `aria-label` string stops being a third phrasing of the same
sentence.

**3. Longer term (not blocking).** The audit asks for the rich tooltip on click
in the editors, where today there is only a native `title` attribute and a
hardcoded "secret value hidden" with no reveal. `VariableTooltip.svelte` is a
plain component and can be mounted from a CodeMirror tooltip extension; the
props it needs are all already computed in the decoration builder.

## Also not mine

* **A5-12 / A5-14** — `lib/workbench/EnvironmentContextMenu.svelte`.
  A5-12: `border-radius: 8px` at line 152 → `var(--radius-8)` (see "what the
  audit got wrong" — the token exists).
  A5-14: the trigger at 82-90 shows only the collection environment; the active
  global environment is in the `title` attribute only, so the button reads "No
  environment" while a global one is supplying variables.
* **A5-05, two of four surfaces.** Response view is
  `lib/workbench/ResponseInspector.svelte:357`, still a `<select>`. The
  JavaScript sandbox picker IS in App.svelte but is outside my region (collection
  overview) and, more importantly, carries `data-testid="sandbox-mode-safe"` and
  `-developer` on the individual buttons, which `SegmentedControl` cannot express
  — it takes one `testId` for the container. Either the tests move to the
  container plus `[data-value]`, or `SegmentedControl` gains a per-option
  `testId`. Both are `lib/ui/` decisions.
* **A5-08, the six variable tables.** `secretMasking.test.mts` pins the literal
  `type={row.variable.secret ? 'password' : 'text'}` expression in App.svelte, so
  routing those inputs through `SecretInput` would regress a test written
  specifically to stop this area regressing. The right sequence is: give
  `KeyValueTable.svelte` (not mine) a `SecretInput`, widen `secretMasking` to
  assert the component rather than the expression, then convert the six tables.

## What the audit got wrong

1. **A5-11 overstates it.** `.field-grid` is not unbounded — `style.css:2712`
   already caps it at `max-width: 620px`. The real divergence was 620px vs the
   680px `.auth-grid` adds, not "capped in two places, unbounded in two". Same
   finding, smaller symptom.
2. **A5-12's premise is wrong.** It says the environment panel's hardcoded 8px
   "doesn't match any defined token — the nearest is `--radius-6`". `--radius-8`
   exists at `style.css:43`. The fix is a one-word substitution, not a judgement
   call about which token is nearest.
3. **A5-04 is a real bug, and worse than "looks like" one.** The audit hedges
   ("this looks like a functional bug"). It is one:
   `internal/core/app_request_build.go:245` is `if auth.APILocation == "query"`
   with header as the else, so a folder that stored `queryparams` was sending the
   API key in a **header** while its own UI said Query params. Silent, wrong, and
   on disk in every folder configured that way. This pass migrates on the next
   save; a one-off Go-side migration would close it for folders nobody edits
   again.
4. **A5-06's "Get new access token" is not implementable in the frontend.** The
   audit proposes the button without noting that the only OAuth2 binding on
   `core.App` is `CompleteOAuth2Callback` — the token store
   (`internal/core/app_oauth2_store.go`) is never exported. `AuthForm` renders the
   button only when an `onFetchToken` prop is supplied, and no call site supplies
   one, so the form does not offer an action it cannot perform. **What Go needs
   to expose**, and the UI is already written against it:

   ```go
   // Fetches (or refreshes) the token for one request's OAuth2 config and
   // returns what the store now holds for it.
   func (a *App) FetchOAuth2Token(collectionID, itemID string) (OAuth2TokenView, error)
   func (a *App) ClearOAuth2Token(collectionID, itemID string) error
   func (a *App) OAuth2TokenView(collectionID, itemID string) (OAuth2TokenView, error)

   type OAuth2TokenView struct {
       AccessToken  string `json:"accessToken"`  // presence only; the UI never renders it
       RefreshToken string `json:"refreshToken"` // presence only
       ExpiresAt    int64  `json:"expiresAt"`    // epoch ms, 0 when unknown
       Error        string `json:"error"`
   }
   ```

   `lib/oauth2TokenStatus.ts`'s `OAuth2TokenRecord` is that shape already. Pass
   it as `tokenRecord` and the status line, the expiry countdown, the refresh
   note and the error state all light up with no further UI work.
5. **A5-07's "secret chips always render `••••`" does not apply to these
   surfaces.** A chip here renders the *reference* (`{{token}}`), never a
   resolved value, so there is no plaintext to mask. Implemented as a lock glyph
   plus the reference text; the `••••` rule is right for anything that inlines a
   resolved value, which none of the three chip surfaces do.
6. **A5-13 undercounts the missing fields.** Beyond placeholders, three fields
   present in `types.OAuth2Auth` were reachable from **no** form at any level:
   `refreshTokenUrl`, `autoFetchToken`, `autoRefreshToken`. `refreshTokenUrl` is
   now in the schema. The two booleans are deliberately still out — see below.

## Left undone, deliberately

* **`autoFetchToken` / `autoRefreshToken` are still not in the form.** They are
  in the model and read by the Go side, but with no token visibility they would
  be two switches whose effect is entirely invisible. They belong in the same
  change as the Go bindings above, next to the status line they govern.
* **The Variables/Secrets `subtabs` in the environment panel** were left as
  `subtabs`. They sit directly beside the folder-settings `subtabs` and behave
  the same way (switch which rows are listed); converting only these would make
  the panel less consistent, not more.
