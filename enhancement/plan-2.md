# Enhancement plan 2: first-run discovery — other API clients, corporate proxy, corporate CA

Status: researched 2026-08-22 against `feat/import-reliability-and-header-suggestions` (e269e21). Companion to
[plan.md](plan.md), which is implemented. Findings below come from vendor docs, upstream source, and checks run
on this machine; the two conclusions that shape everything are stated first because they cancel most of the
obvious design.

---

## 0. Two findings that decide the scope

### Postman's local store cannot be read, and mostly holds nothing

Four independent blockers, any one of which is fatal:

1. **Collections require sign-in.** Scratch Pad, the genuinely-offline mode, was sunset. Signed out there are
   no collections on disk at all; signed in they are cloud-owned, and signing out deletes the local copy.
2. **No freshness guarantee.** [postman-app-support#13598](https://github.com/postmanlabs/postman-app-support/issues/13598)
   ("Lack of Reliable Offline Access to Collections") is open and unassigned. Promising "your collections" and
   showing an undefined subset is worse than not offering.
3. **Not parseable in Go.** Chromium IndexedDB over LevelDB using the custom `idb_cmp1` comparator — stock
   goleveldb will not open it — with values as V8 structured-clone blobs. No Go deserializer exists; the mature
   tooling is [google/dfindexeddb](https://github.com/google/dfindexeddb), Python, self-described experimental.
4. **The lock.** LevelDB takes an exclusive OS lock. Postman will be running: that is the premise of the
   feature.

Prior art is unanimous — **no API client reads Postman's app data.** Bruno, Insomnia, Yaak, Hoppscotch and
Thunder Client all take a file. The ToS position is genuinely arguable (the AUP bans "decode the Software" and
"scrape, data mine, extract"; the clauses bind the account holder, not us) but it never becomes the deciding
question.

Note the asymmetry: **Postman's own desktop app auto-detects Insomnia's and Thunder Client's directories.** The
product pattern is uncontroversial. Postman's store is simply the one that cannot be read.

**Therefore: presence detection only for Postman.** `os.Stat` the config directory, never open a file inside
it, and use the fact only to tailor what we offer — their Data Export dump, or an API key. plan.md already
taught the importer to read data dumps and ZIP exports, so the destination exists.

### Half of the proxy/CA request already works

Verified on this machine:

- `x509.SystemCertPool()` returns **122 CAs**, and `Spec.Build`
  ([transport/cache.go:237](../internal/transport/cache.go:237)) leaves `TLSClientConfig` alone unless
  verification is off or a custom CA is set — so Go uses the platform verifier. **A corporate CA installed in
  the OS trust store already works, with no feature at all.**
- The default proxy mode is already `system` ([core/app.go:917](../internal/core/app.go:917),
  [prefs/prefs.go:420](../internal/prefs/prefs.go:420)), and `http.ProxyFromEnvironment`-equivalent handling in
  `proxyURLFromEnvironment` ([transport/proxy.go:132](../internal/transport/proxy.go:132)) honours
  `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` with `NO_PROXY` suffix bypass. **A corporate machine that sets the
  environment variables already works.**

So "pull them in by default" is, for the common corporate case, already true. The genuine gaps are narrow:

| Gap | Evidence |
|---|---|
| Windows OS proxy settings never read | `SystemProxyURLForRequest` has a `darwin`-only branch, [proxy.go:126](../internal/transport/proxy.go:126) |
| Linux desktop proxy settings never read | same; `gsettings get org.gnome.system.proxy mode` works on this machine |
| PAC only discovered from `LITEAPI_SYSTEM_PAC_URL` | [proxy.go:119](../internal/transport/proxy.go:119) — an env var no corporate machine sets; the OS holds the real PAC URL |
| No CA **file** discovery | nothing reads a Zscaler/Netskope PEM sitting on disk outside the store |
| No first-run surface at all | no wizard, no welcome screen, no "already asked" flag anywhere |

---

## 1. Rules that must hold (read before writing any code)

These are not preferences; violating one turns a convenience feature into a security or privacy incident.

1. **Detect presence, then ask, then read.** Enumerating a directory to see *that* a client is installed is
   fine. Opening the files inside it is not, until the user has said yes. An unprompted "we found your
   collections" banner means we already read files holding live bearer tokens.
2. **Never adopt a CA automatically.** A proxy is adopted silently by curl and every browser, and already is
   here. A CA is different: silently trusting a PEM found on disk converts any stray file into blanket trust —
   exactly the MITM shape. Show subject, issuer, expiry and SHA-256 fingerprint, and require a click.
3. **Never decrypt another app's secrets.** Bruno's `secrets.json` is derivable (AES keyed on
   `sha256(machineId)` when Electron `safeStorage` is unavailable). Deriving it is credential exfiltration.
   Import secret values as empty placeholders and say so.
4. **Never write to another app's files.** Read-only, always. Copy before parse where a lock is possible.
5. **Ask once.** A prompt that returns every launch is a prompt people learn to dismiss without reading.
6. **Discovery must never block or fail startup.** Bounded, cancellable, and its failure is silence.

---

## 2. Work items

### Item 1 — OS proxy discovery on Windows and Linux

Files: `internal/transport/proxy_windows.go` (build-tagged), `internal/transport/proxy_linux.go`,
`internal/transport/proxy_settings.go` (portable parsing), tests in `internal/transport/`.

The existing macOS path is the model to copy: `scutil --proxy` is shelled out, and its **output parsing is a
pure function** (`ProxyURLFromMacOSScutilOutput`, [proxy.go:161](../internal/transport/proxy.go:161)) tested
without a Mac. Do the same so Windows behaviour is testable on Linux CI.

- **Windows**: `golang.org/x/sys/windows/registry` is already a direct dependency (v0.42.0, in the module
  cache; `GOOS=windows` cross-build verified). Read `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet
  Settings`: `ProxyEnable` (DWORD), `ProxyServer` (either `host:port` or the
  `http=host:port;https=host:port;socks=host:port` per-scheme form), `ProxyOverride` (`;`-separated, with the
  special token `<local>` meaning bypass anything without a dot), and `AutoConfigURL` for PAC.
- **Linux**: shell out to `gsettings get org.gnome.system.proxy mode` → `none` | `manual` | `auto`; then
  `org.gnome.system.proxy.http host/port`, `.https`, `.socks`, `org.gnome.system.proxy ignore-hosts`, and
  `autoconfig-url` for `auto`. Absent `gsettings` (KDE, headless, a container) is a normal outcome, not an
  error. Same 2 s timeout as the macOS path.
- **PAC from the OS**: both branches, plus the existing macOS `ProxyAutoConfigURLString`, should feed the PAC
  evaluator that already exists (`ResolvePACProxyURL`, [proxy.go:263](../internal/transport/proxy.go:263)) —
  today only an env var can reach it.
- Environment variables keep priority over OS settings: an explicitly exported `HTTPS_PROXY` is a deliberate
  act and must win.

Tests: table-driven over registry value shapes and `gsettings` output, including `<local>`, per-scheme
`ProxyServer`, `ProxyEnable=0` with a populated `ProxyServer` (set but off — must not be used), an empty
`ignore-hosts`, and a PAC URL in each source. Plus: env var beats OS setting.

### Item 2 — Client discovery (presence, then contents on consent)

New package `internal/discovery`. Pure functions over an injectable filesystem root so every case is testable
without the client installed — none is installed on the development machine, and CI will never have one.

Two distinct operations, and the split is the privacy boundary:

- `DetectInstalled(home) []Installation` — `os.Stat` only. Returns client name, config path, and whether we
  can read its collections. **Never opens a file.**
- `ReadCollections(installation) ([]Discovered, []string, error)` — called only after consent.

| Client | Path (macOS / Linux / Windows) | Format | Read |
|---|---|---|---|
| Postman | `~/Library/Application Support/Postman`, `~/.config/Postman`, `%APPDATA%\Postman` | LevelDB | **presence only** |
| Insomnia | `.../Insomnia/`, `$INSOMNIA_DATA_PATH` | NeDB (newline JSON) | yes |
| Bruno | index in `<userData>/bruno/preferences.json` + `workspaces.lastOpenedWorkspaces` → `workspace.yml` | folders of `.bru` | yes |
| Thunder Client | VS Code `globalStorage/rangav.vscode-thunder-client/` (+ Insiders, VSCodium, `~/.vscode-server`, workspace `thunder-tests/`) | plain JSON | yes |
| Yaak | `~/.local/share/app.yaak.desktop/db.sqlite` | SQLite | **presence only** (see below) |

Reuse what exists rather than writing new converters:

- **Bruno** needs no importer at all: this app already opens Bruno collection folders
  (`readCollectionFromDisk`, and `collectionImportFolderLooksSupported` already checks `bruno.json` /
  `collection.bru` / `opencollection.yml`). Discovery only has to produce the *paths*.
- **Insomnia** already has an importer (`internal/importers/insomnia.go`) that reads the **export** shape. The
  NeDB reader's job is to fold `insomnia.*.db` records into that shape and hand it over — one converter, not
  two. NeDB is append-only newline JSON with `{"$$deleted":true}` tombstones: read lines, unmarshal, fold by
  `_id`, tolerate a torn trailing line (Insomnia's own reader does).
- **Thunder Client** needs a small converter. Three layouts exist: current `collections/tc_col_*.json`, older
  flat `thunderCollection.json`, and `ThunderCollection.db` which is JSON despite the extension.
- **Yaak** is plain SQLite with no encryption, but adding a SQLite driver is a new dependency that cannot be
  fetched offline here. Presence detection now; reading it is a follow-up once the dependency question is
  settled.

Secrets: Insomnia stores auth credentials in plaintext and Bruno encrypts them. Import **names without
values** for anything secret-shaped, and return a warning naming what was blanked, so nobody discovers the gap
by sending a request that 401s.

### Item 3 — CA candidate detection (offer, never adopt)

`internal/discovery/cacerts.go`. Look for a PEM that is **not** already in the system pool — a CA present in
the store needs nothing from us, so offering it is noise:

- Linux: `/usr/local/share/ca-certificates/*.crt`, `/etc/pki/ca-trust/source/anchors/*`
- macOS: the well-known MDM drop locations; the keychain is already trusted, so it is out of scope by
  definition
- Windows: the enterprise store is already used by the platform verifier — presence detection only

For each candidate, parse with `crypto/x509` and report **subject, issuer, not-after, and SHA-256
fingerprint**, plus whether `SystemCertPool` already contains it. Expired or already-trusted candidates are
reported as such and not offered. Adoption sets the existing
`Preferences.Request.CustomCaCertificate{Enabled,FilePath}` — the plumbing, including "keep system roots", is
already complete and cache-keyed on both path and content.

### Item 4 — First-run surface

- **Backend**: `ensureReadyLocked` already computes `freshState` from a missing state file
  ([app_bootstrap.go:68](../internal/core/app_bootstrap.go:68)) — the only existing first-launch signal. Add a
  persisted `Preferences.General.DiscoveryPromptedAt` so the offer is made once (rule 5). New bound methods:
  `DiscoverImportSources()` (presence + proxy + CA candidates, cheap, no file reads),
  `ImportDiscoveredCollections(selections)`, `AdoptDiscoveredProxy()`, `AdoptDiscoveredCACertificate(path)`,
  `DismissDiscoveryPrompt()`.
- **Frontend**: a modal built on the existing `Modal.svelte` shell — `GitNotFoundModal.svelte` is the closest
  precedent, an "environment detection" dialog gated on a truthy value. Sections for collections, proxy and CA,
  each independently selectable, each defaulting **unchecked** for anything that reads files or changes trust.
  The notification centre is display-only (no action field), so it cannot carry this; it can carry the outcome.
- Reachable afterwards from the Import panel, so dismissing is not permanent.

---

## 3. Order, and what "done" means

1. Item 1 (no consent questions, immediate value on corporate Windows/Linux)
2. Item 2 detection + Bruno/Insomnia/Thunder Client reading
3. Item 3 CA candidates
4. Item 4 bindings and modal

Done means: `go test ./...`, `npm test`, `npm run check`, `npm run build` green; `GOOS=windows` and
`GOOS=darwin` cross-builds green; every discovery path exercised against a synthetic fixture tree rather than
an installed client; and no code path reads a file from another application before an explicit user action.
