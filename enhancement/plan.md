# Enhancement plan: Postman import reliability, import error messages, header autocomplete, TLS errors

Status: **implemented 2026-08-22** on `feat/import-reliability-and-header-suggestions` (7 commits on top of
bedd544). All seven work items are done, with tests written first; see "Outcome" at the foot of this file for
what changed against the plan. Researched against `main` (bedd544); every defect below was **reproduced with
code**, not inferred. Work items are ordered; each lands as its own commit with tests written first (TDD). Run
`go test ./...` and `cd frontend && npm test && npm run check` before every commit.

Conventions you must follow
- Go: tests beside the code (`*_test.go`). Frontend: pure logic in `frontend/src/lib/*.ts`, tested with
  `node --test` in `frontend/test/*.test.mts` (no vitest/playwright). `scripts/verify-inputs.mjs` enforces
  floors on test-file/module counts – add, never delete, tests.
- Error strings returned to the UI must not contain filesystem paths or source content
  (see `collectionImportDiagnostic` and `TestCollectionImportDiagnosticsDoNotExposePathTokens`). Keep that
  guarantee while making messages useful.
- Don't “fix” by loosening a test. If a test encodes the wrong behaviour, change the test *and* explain why
  in the commit message.
- Commit messages in this repo are one plain sentence describing the user-visible effect (see `git log`).

---

## 0. Root-cause summary (read first)

| # | Symptom the user sees | Root cause | Evidence |
|---|---|---|---|
| A | Every import failure says **“selected import could not be read safely”**; must open console to learn why | `collectionImportDiagnostic` (`internal/core/collection_import.go:646`) allow-lists ~20 literal strings and collapses *everything else* to that one sentence. Apply failures likewise collapse to “selected imports could not be committed” (`collection_import_apply.go:138`). The UI (`frontend/src/lib/views/ImportPanel.svelte:140`) renders `row.error` fine – it is simply handed nothing useful. | Probe: 8 valid Postman shapes → all produce that string |
| B | Valid Postman exports are rejected | `importers.ImportPostman` (`internal/importers/postman.go:19-126`) unmarshals into rigid structs. Postman v2.1 allows `request`, `header`, `response[].header` to be strings; `response[].code` is sometimes a string; header `value` may be a number/null; `body.raw` may be an object; Windows exports carry a UTF‑8 BOM. `json.Unmarshal` aborts on the *first* mismatch, so one odd saved example discards a 500-request collection. | `detectCollectionImport` on: request-as-string, header-as-string, response-header-string, response-code-string, header-value-number, body-raw-object, BOM → all `json: cannot unmarshal …` |
| C | Postman environment / globals / v1 / data-dump files: “source is ambiguous; choose an import kind manually” – and no override helps | Detection (`collection_import.go:321-422`) only recognises `info`+`item`. `_postman_variable_scope`, v1 `requests[]`, dump `collections[]` are never checked. A parser for Postman env files *does* exist (`internal/store/bru/environments.go:180`, reached via `ImportGlobalEnvironment`) but only from the Environments-panel paste box. The file picker advertises `*.zip` then refuses it. | Probe: environment/globals/v1/data-dump → “ambiguous” |
| D | Manual “Postman” override on a non-collection reports **“1 imported”** and writes an empty collection folder | `ImportPostman` returns success for any JSON object; no `info`/`item` sanity check (`postman.go:32`). `collection_import_apply.go:95-100` trusts it. | Probe: env file + override → `applied=1`, 0 requests, directory created |
| E | A single long request name fails the **whole** batch with “could not be committed” | `scalar.SanitizeFilename` (`internal/scalar/scalar.go:83`) has no length cap → `ENAMETOOLONG`. ~85 CJK characters suffice. Windows reserved names (`CON`, `NUL`, …) also unhandled. | Probe: 300-char name → `APPLY ERR selected imports could not be committed` |
| F | Import “succeeds” but data is wrong/missing, no warning | `postmanURL` returns the literal `{{host}}` for a `url` object without `raw` (`postman.go:567`); `body.mode:"file"` dropped (`:624`); `variable[].disabled` ignored (`:381`); sibling folders with the same name merge (`:556`); `protocolProfileBehavior` (incl. `strictSSL`) ignored; empty folders vanish; collection `description` dropped. `ApplyCollectionImport` never emits a notification; `previewImportSources` silently returns when no destination workspace (`App.svelte:3257`). | Code reading + probe |
| G | No header-name suggestions | `KeyValueTable.svelte:227` name cell is a bare `<input>`; no header list exists anywhere in the repo. Reusable pieces: `lib/commandPalette.ts` (`filterCommands`, `moveSelection`), listbox/`aria-activedescendant` markup in `lib/modals/search/CommandPaletteModal.svelte:38-41`, outside-pointerdown dismissal in `lib/sidebar/SidebarActionMenu.svelte:64-71`, MIME map in `lib/contentTypes.ts`. | Survey |
| H | HTTPS request that works in Postman fails here with a cert error (seen on macOS; same on Linux) | Not a platform bug. Postman ships with *SSL certificate verification OFF*; this app defaults ON (`prefs` `SSLVerification=true`, per-request `Settings.VerifyTLS=true` in `types/request_item.go:87`). Go’s macOS verifier does honour keychain CAs, so corporate CAs installed in the keychain work; self-signed/mis-issued certs (CN-only, SHA-1, hostname mismatch) fail. The user sees the raw Go error `Post "https://…": tls: failed to verify certificate: x509: certificate signed by unknown authority` with no pointer to Preferences → SSL verification / Custom CA, or the per-request Verify TLS toggle. Postman’s per-collection/request `protocolProfileBehavior.strictSSL=false` is dropped on import, so a collection that deliberately disables verification in Postman is imported as verify-on. | Test against `httptest.NewTLSServer` |

Why tests missed all of this: every import test feeds a one-request toy JSON; the single override test overrides a
*correct* Postman body; there is no real-world Postman fixture; TLS tests only check the toggle works, not the message.

---

## 1. Tolerant Postman parser (fixes B, D, part of F)

Files: `internal/importers/postman.go`, new `internal/importers/postman_shapes.go`, tests
`internal/importers/postman_shapes_test.go`, new fixture `docs/qa/import-fixtures/postman-realworld.json`.

1. Write the fixture first: one collection containing **every** shape in the table under B plus nested
   folders, folder auth/events, a v2.0-style `auth` object, `url` object without `raw`, `body.mode:"file"`,
   disabled variable, `protocolProfileBehavior:{strictSSL:false}` at collection and request level, an empty
   folder, a collection-level description object, and a UTF‑8 BOM. Add a test that imports it and asserts
   request count, URLs, headers, every auth mode, and zero errors.
2. Replace rigid field types with small types that implement `json.UnmarshalJSON`:
   - `postmanRequestRef` – string (URL shorthand → GET) or object.
   - `postmanHeaderList` – `[]` of objects **or** strings (`"Key: value"`), or a single string with newlines.
   - `postmanFlexString` – string/number/bool/null → string (header/param values, `body.raw`, `response.body`).
   - `postmanFlexInt` – int or numeric string (`response.code`).
   - `postmanExec` already handles string|[]string via `interface{}` – keep.
   - Strip a leading BOM before `json.Unmarshal` (do it in `detectCollectionImport` too, before `yaml.Unmarshal`).
3. Do **not** abort the whole collection on an item-level problem: unmarshal `item` as `[]json.RawMessage`,
   decode each; on failure record a warning `request "<name>" in folder "<path>" was skipped: <reason>` and
   continue. `ImportPostman` grows a `[]string` warnings return (thread through `collectionFromImport` →
   `detectCollectionImport` which already returns warnings → `row.Warnings`, already rendered by
   `ImportPanel.svelte`).
4. Reject non-collections: if the document has neither `info` nor `item` return
   `errors.New("not a Postman collection: missing info and item")`. Add a test that override=postman on
   `{"hello":1}` and on an environment file yields a row error, not an empty import (this is the test that
   currently cannot exist – write it to fail first).
5. `postmanURL`: when `raw` is absent, rebuild from `protocol`/`host`/`port`/`path`/`query`; only fall back to
   `{{host}}` with a warning. `body.mode:"file"` → `Body.Mode="file"` with the `file.src` path. Honour
   `variable[].disabled`. Uniquify sibling folder paths (`Users`, `Users 2`) the same way requests are.
   Import `protocolProfileBehavior.strictSSL=false` → `item.Settings.VerifyTLS=false` (collection-level value
   applies to every request lacking its own). Keep collection `info.description` (string or `{content}`) as
   `collection.Docs`. Each of these is its own small test.

## 2. Real error messages end to end (fixes A)

Files: `internal/core/collection_import.go` (`collectionImportDiagnostic`), `collection_import_apply.go`,
`internal/importers/postman.go`, `frontend/src/App.svelte`, `frontend/src/lib/views/ImportPanel.svelte`.

1. Introduce a typed error: `type ImportDiagnostic struct{ Kind, Message string; Line, Column int }` in
   `internal/importers`. Importers wrap parse failures: for `*json.SyntaxError` compute line/column from
   `Offset`; for `*json.UnmarshalTypeError` say `field "<Field>" expected <Type> but found <Value>`.
2. Rewrite `collectionImportDiagnostic` to *sanitise instead of allow-list*: keep the message, but scrub
   anything that looks like a path (`/`, `\`, `~`, drive letters) and anything longer than ~200 chars. Keep the
   allow-listed strings as-is. Extend `TestCollectionImportDiagnosticsDoNotExposePathTokens` with inputs
   whose Go error embeds a path (`os.PathError`) and assert the scrubbed output still names the cause
   (“Postman JSON is invalid at line 42, column 7: invalid character '}'”).
3. Apply path: keep the rollback, but return `fmt.Errorf("selected imports could not be committed: %s",
   sanitised(err))`. Map `ENAMETOOLONG`/`syscall.ENAMETOOLONG` → “a request or folder name is too long for
   this filesystem (request "<name>")`. Surface rollback-restore failure (`rollbackCollectionImportMutations`
   currently `_ =`s it) as a notification naming the backup folder suffix.
4. Emit a notification (`a.notify`, as `ImportGlobalEnvironment` does at `app_environments.go:378`) on apply:
   “Imported 3 collections (1 warning)” / “Import failed: …”. Frontend: `previewImportSources` must set
   `importStatus = 'Choose a destination workspace first'` instead of returning silently; show
   `row.warnings` count in the results block; stop `runAction` from wiping an import error with a later
   unrelated action (scope the import error to `importStatus`).
5. Log the *unsanitised* error with `log.Printf("import: …")` on the Go side so the console still has the
   full story for bug reports.

## 3. Postman environment, globals, v1, data-dump detection (fixes C)

Files: `internal/core/collection_import.go`, `collection_import_apply.go`, `collection_import_types.go`,
`internal/store/bru/environments.go`, `collection_import_dialog.go`, `frontend/src/lib/views/ImportPanel.svelte`,
`frontend/src/lib/importPlanning.ts`, `frontend/wailsjs` (regenerate bindings: `wails generate module` or
`go run ./tools/...` – check `qa/bindings.sh`).

Decision already made by the owner: a standalone Postman environment/globals file imports as a **workspace
global environment** (same destination as today’s paste path), not attached to a collection.

1. Detection: `_postman_variable_scope` ∈ {environment, globals} or (`values[]` + `name`) → kind
   `postman-environment`; `requests[]`+`folders[]`/`order[]` → `postman-v1` with error “Postman v1
   collections are not supported; re-export as Collection v2.1 from Postman”; `collections[]`/`environments[]`
   top-level → `postman-dump` → expand into one preview row per contained collection + environment (treat
   like a multi-source). `.zip` → either implement (Postman dump zips are just JSON files: unzip in memory,
   re-dispatch) or remove `*.zip` from the picker filter at `collection_import_dialog.go:20`. Implement –
   it’s ~40 lines with `archive/zip` and the size limits already exist.
2. Preview row gets `DetectedKind:"postman-environment"`, `Environments:[{name}]`, no requests/folders, and
   the destination column shows “Workspace globals”. Apply routes it through the existing
   `ParseImportedGlobalEnvironments` → append to `workspace.GlobalEnvironments` (rename-on-conflict like
   collections). Reuse `CreateGlobalEnvironment`/`UpdateGlobalEnvironmentVariables` internals rather than
   duplicating persistence.
3. Tests: detection matrix rows for each new kind; apply test asserting the global environment exists after
   relaunch; hash-guard test still holds for the new path.

## 4. Filename safety (fixes E)

File: `internal/scalar/scalar.go`, `internal/scalar/scalar_test.go`.
- Cap `SanitizeFilename` output at 120 **bytes** (not runes) cut on a rune boundary, then append a short
  deterministic suffix when truncation happened (first 6 hex of `DeterministicID`) so two long names stay
  distinct. Reject Windows reserved basenames (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`) by suffixing `_`.
- Tests: 300-char ASCII, 200-char CJK, `CON`, two names differing only after byte 120. Then an apply-level test
  that the 300-char Postman collection imports with the request written to disk.

## 5. Header name/value autocomplete (G)

Files: new `frontend/src/lib/httpHeaders.ts` + `frontend/test/httpHeaders.test.mts`, new
`frontend/src/lib/SuggestionListbox.svelte`, edit `frontend/src/lib/KeyValueTable.svelte` and the headers
instance at `frontend/src/App.svelte:8316`.

1. `httpHeaders.ts`: export `REQUEST_HEADER_NAMES` (canonical casing) – at minimum: Accept, Accept-Charset,
   Accept-Encoding, Accept-Language, Access-Control-Request-Headers, Access-Control-Request-Method,
   Authorization, Cache-Control, Connection, Content-Disposition, Content-Encoding, Content-Language,
   Content-Length, Content-Type, Cookie, Date, DNT, Expect, Forwarded, From, Host, If-Match,
   If-Modified-Since, If-None-Match, If-Range, If-Unmodified-Since, Idempotency-Key, Keep-Alive, Origin,
   Pragma, Prefer, Proxy-Authorization, Range, Referer, Sec-Fetch-Mode, TE, Trailer, Transfer-Encoding,
   Upgrade, Upgrade-Insecure-Requests, User-Agent, Via, X-API-Key, X-Correlation-ID, X-CSRF-Token,
   X-Forwarded-For, X-Forwarded-Host, X-Forwarded-Proto, X-HTTP-Method-Override, X-Request-ID,
   X-Requested-With. Export `HEADER_VALUE_SUGGESTIONS: Record<string,string[]>` for Content-Type/Accept
   (derive from `contentTypes.ts` + `application/json`, `application/x-www-form-urlencoded`,
   `multipart/form-data`, `text/plain`, `application/xml`, `*/*`), Authorization (`Bearer `, `Basic `),
   Cache-Control (`no-cache`, `no-store`, `max-age=`), Accept-Encoding (`gzip, deflate, br`), Connection
   (`keep-alive`, `close`). Export `suggestHeaderNames(query, existingNames)` (case-insensitive prefix first,
   then substring; exclude names already present in the table; max 8) and `suggestHeaderValues(name, query)`.
   Tests: prefix beats substring, case-insensitivity, exclusion of existing rows, empty query returns the
   common six (Content-Type, Authorization, Accept, User-Agent, Cache-Control, Cookie), cap of 8.
2. `SuggestionListbox.svelte`: props `items: string[]`, `activeIndex`, `anchor` (the input), `onPick(value)`,
   `onClose()`. `role="listbox"`/`role="option"`/`aria-activedescendant` as in `CommandPaletteModal`;
   outside-`pointerdown` dismissal as in `SidebarActionMenu`; positioned under the input with
   `position: fixed` + `getBoundingClientRect` so it escapes the table’s overflow.
3. `KeyValueTable.svelte`: new optional props `nameSuggestions?: (query, rows) => string[]` and
   `valueSuggestions?: (name, query) => string[]`. In the name cell: on input/focus compute suggestions; keys:
   ArrowDown/ArrowUp move (reuse `moveSelection` from `commandPalette.ts`), Enter/Tab accept (Tab then moves
   focus to value cell), Escape closes; `aria-autocomplete="list"`, `aria-expanded`. Only the headers
   instance in `App.svelte` passes the functions; other instances are unchanged. Selecting a name that has
   value suggestions opens them in the value cell immediately.
4. Manual check via `/run`: type `con` → Content-Type offered; Enter → value list shows `application/json`.

## 6. TLS failures: actionable message + Postman parity (fixes H)

Files: `internal/core/app_execute_http.go`, `internal/importers/postman.go` (done in §1.5 for `strictSSL`),
`frontend/src/lib/views/preferences/GeneralSection.svelte`, response error rendering in `App.svelte`.

1. Classify transport errors: `errors.As(err, &x509.UnknownAuthorityError{})`, `x509.HostnameError`,
   `x509.CertificateInvalidError`, `*tls.CertificateVerificationError`. Replace `result.Error` with
   `TLS certificate verification failed for <host>: <reason>. Fix: install/select the CA under Preferences →
   Request → Custom CA certificate, or turn off “Verify TLS” for this request (Settings tab) / globally
   (Preferences → SSL certificate verification). Postman has verification off by default.` Keep the raw Go
   error after a newline for support. Test: `httptest.NewTLSServer` → assert message names the host and
   mentions both remedies; second test with hostname mismatch.
2. Response pane: when `response.error` contains the TLS marker render two buttons “Disable Verify TLS for
   this request and resend” and “Open preferences”. Pure mapping (`tlsErrorActions(error)`) in a new
   `lib/tlsErrors.ts` with a node test; the buttons call existing `UpdateRequest`/`SendRequest`.
3. macOS specifics to verify, not assume: on a Mac with a corporate CA in the *login* keychain marked
   “Always Trust”, Go’s platform verifier accepts it – add a `docs/qa` note with the `security add-trusted-cert`
   command for users whose CA is only in a file. If the owner confirms a case where Postman succeeds and we
   fail **with verification on and the CA in the keychain**, capture `openssl s_client -connect host:443
   -showcerts` output and compare the chain; suspects are a missing intermediate (Postman/Node fetches AIA,
   Go does not) – document as a known limitation with the custom-CA workaround.
4. Do **not** change the default to verification-off; instead make the first-run Preferences copy say
   “Postman disables this by default; LiteAPI verifies certificates unless you turn this off”.

## 7. Lower-priority fidelity (from the trace; do after 1–6)

- Write `folder.bru` during import materialisation (`app_collection_io.go:76-162` never calls
  `writeFolderConfigLocked`) so folder auth/scripts survive reopen-from-folder/git clone. (They *do* survive a
  normal relaunch via app state – verified – so this is fidelity, not data loss.)
- Environment filenames collide on write (`app_collection_io.go:140-148`, no dedup map).
- Hash guard is bypassed by the override/translate re-read paths (`collection_import_apply.go:73,89`) – re-hash.
- Detection matrix test covers 4 kinds; extend to every branch of `detectCollectionImport`.

---

## Definition of done

- `docs/qa/import-fixtures/postman-realworld.json` imports with 0 errors and every request reachable.
- Dropping a Postman environment JSON onto the Import panel produces a workspace global environment.
- Every import/commit failure message names the cause (line/column, field, request name, or filesystem
  reason) and contains no path. `TestCollectionImportDiagnosticsDoNotExposePathTokens` still passes.
- A 300-character request name imports.
- Typing in a header name cell offers suggestions; Enter fills it; value suggestions follow for Content-Type.
- A self-signed HTTPS request shows the actionable TLS message with working one-click remedies.
- `go test ./...`, `npm test`, `npm run check`, `npm run build` green; `qa/bindings.sh` green if bindings changed.

---

## Outcome (2026-08-22)

All seven items implemented and validated. `go test ./internal/...`, `npm test` (892 tests), `npm run check`
and `npm run build` are green. The header completion was additionally driven in a browser against the real
`KeyValueTable` component: typing `content-ty`, Enter, Enter produced `Content-Type: application/json` without
the mouse.

Where the work departed from the plan, and why:

- **The TOCTOU concern (item 7, bullet 3) was not real.** The manual-override path re-reads the file, but the
  candidate it compares against is *also* rebuilt at apply time, so a swapped file fails the hash comparison
  already. `TestOverrideRereadStillHonoursTheContentHash` now records that, since the reasoning is not local
  to either function.
- **Folder metadata (item 7, bullet 1) was less severe than described.** Folder auth and scripts do survive a
  normal relaunch, because app state is persisted separately from the collection files. They were lost on
  reopen-from-folder, clone, and git — which is what the fix addresses.
- **`json.SyntaxError` positions** had to be computed in the importer rather than in `collectionImportDiagnostic`,
  because only the importer still holds the document the byte offset refers to.
- **ZIP detection keys off content, not the extension**, after the existing
  `TestCollectionImportManualOverrideRescuesDetectionError` caught the first attempt: a Postman collection that
  someone had named `.zip` must stay rescuable by the manual override.
- **The dump detector had to be tightened** during item 7: a Bruno JSON collection carries `environments` under
  the same key a Postman dump does, and the first version would have split one into rows that are not
  collections.
- **`x509.HostnameError.Error()` nil-dereferences its certificate field.** Found while testing item 6; rendering
  the underlying error is now defensive, since a diagnostic string is no place to crash.
- **One pre-existing problem was left alone**: `qa/bindings.sh` fails identically at bedd544, before any of this
  work. The diff is pure reordering — a `sort` collation mismatch between the machine that generated
  `qa/baseline/bindings.txt` and this one. The bound-method count is unchanged at 189, and no bound method was
  added or altered here. Worth fixing (pin `LC_ALL=C` in the script), but it is not this branch's to fix.
- `TestMockServerLifecycleThroughTheBindings` is flaky under the full-package run (a port-rebind race); it
  passes in isolation and on repeated full runs, at this branch and at the base.
