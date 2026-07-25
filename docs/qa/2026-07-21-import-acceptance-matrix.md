# Collection Import Acceptance Matrix

Status: **Required after import slice 2b root review**

Package under test: `build/bin/LiteAPI.app` at the final post-sign executable hash only.

Fixture root: `docs/qa/import-fixtures`. Every native run copies source fixtures into a fresh retained temporary directory before interaction; source and destination hashes are recorded where mutation matters.

## Automated matrix

| Case | Input | Expected result |
| --- | --- | --- |
| Picker cancel | Helper returns no paths | `cancelled=true`; no preference, AppState, or filesystem change |
| Picker multiple | Primary + Unicode + invalid | Original order retained; valid rows ready; invalid row-scoped error; no contents in returned data |
| Remembered directory | File picker then folder picker | Only canonical directory persisted; no selected filename/content; restored on relaunch |
| Folder preview | `bruno-folder/` | Whole-collection open semantics; hierarchy/environment summary; no source copy or mutation |
| Format detection | Postman, Insomnia, OpenAPI 3, BRU, cURL | Exact detected kind and non-empty collection/request preview |
| Recognized unsupported | Swagger 2, WSDL, Yaak, ZIP/OpenCollection bundle fixtures | Specific row error; never ready; no empty collection |
| URL success | Local HTTP fixture, including query token | One fetch per preview/apply; safe displayed URL excludes query; content hash validated |
| URL redirect/size/status | Local redirect chain, loop/excess, >16 MiB, non-2xx | Bounded deterministic row error; token/body/path absent |
| Paste fallback | Ambiguous name plus valid Postman body | Advanced source only; manual override rescues; raw content is not persisted |
| Override + subselection | Rescued source, one child request selected | Stable re-preview IDs; exactly the selected request materializes |
| Hierarchy selection | Environments/folders/requests all/none/mixed | Explicit empty filters honored; no duplicate summaries |
| Destination/conflicts | none, exists, already-open, unavailable | Correct preview state; rename/skip/replace exact; unavailable cannot apply |
| Replace confirmation contract | Existing target | No replace occurs without explicit confirmed selection from UI |
| Stale input | File/folder/URL changes after preview | Apply rejects only the stale selection without creating output |
| Mixed batch | Valid + invalid, select valid | Valid sources commit; invalid stays row-scoped; result lists both truthfully |
| Batch failure | Two valid sources; injected second write/rename/persist failure | State, watchers, targets, backups, and staging fully roll back |
| Duplicate selection/source | Repeated IDs | Rejected before mutation |
| Symlink/path escape | Source/destination symlink and parent escape | Rejected; no out-of-root write |
| Large/tree limits | 16 MiB+ file, 4096+ tree, 64 MiB+ tree | Bounded error; no partial output |
| Relaunch/round trip | Import, export, reimport, restart | Collection paths, hierarchy, environments, auth/scripts, request file paths persist faithfully |

## Packaged-native matrix

The independent native QA agent performs these through visible UI and uses read-only filesystem checks after each material operation.

1. Open File > Import and confirm the Files tab, `Choose files…`, `Choose collection folder…`, and drop target are visible and keyboard named.
2. Cancel the native multi-file picker; confirm focus returns and sidebar/disk/state are unchanged.
3. Select `postman-primary.json`; verify detected Postman, nested folder and two requests, destination, no conflict, then import and relaunch.
4. Select `postman-primary.json`, `postman-unicode.json`, and `invalid.json` together; verify order, Unicode, two valid rows, one row-scoped error, deselect one request, and apply selected valid rows.
5. Choose `bruno-folder/`; verify whole-folder semantics, visible hierarchy/environment, source hashes unchanged, immediate sidebar/open-tab update, and relaunch.
6. Drop a copied OpenAPI fixture on the focused drop target; verify parity with the picker and apply it.
7. Use URL mode against a deterministic local fixture URL containing a benign query marker; verify the marker is absent from accessibility/UI/state evidence, preview/apply succeeds, and a non-2xx URL stays row-scoped.
8. Use Paste mode with the cURL fixture; verify cURL detection, method/headers/body preview, import, and no pasted content in persisted preferences/state.
9. Exercise rename, skip, and confirmed replace against a copied conflict fixture; verify exact destination paths and no backup/staging residue.
10. Modify a copied source after preview; Apply must reject it without a collection. Cancel/dismiss the replace confirmation and verify no mutation.

## Evidence required

- Process-attributed isolated data directory and final executable hash.
- Screenshot plus accessibility tree for picker entry, mixed preview, expanded hierarchy, URL safe display, result list, and failure state.
- Before/after source hashes for folder and stale-source cases.
- Destination tree listing and absence of `.liteapi-import-*`/backup residues.
- Persisted state search proving no URL query marker, pasted content, or fixture secret marker.
- Same-directory relaunch evidence for successful imports and a fresh-directory non-leak check.

Any reproducible defect enters the mandatory same-agent retest loop and resets final-package acceptance if it changes the executable.

