# File-First Collection Import Plan

Status: **Root-approved for bounded implementation after backend contract review**

## Product standard

The workflow uses Yaak’s short path—open Import, select a file, review, import—with Bruno’s local-first breadth: multiple files, folders, drag/drop, detection, conflict handling, environments, and filesystem-safe output. It remains recognizably LiteAPI and requires no account or cloud service.

## Information architecture

`File → Import…`, the command bar, and the workbench action all open the same Import view. They do not maintain separate state or handlers.

The view has four progressively disclosed source modes:

1. **Files** — default and primary.
2. **URL** — secondary.
3. **Paste** — advanced fallback.
4. **Git repository** — existing local-first clone/open workflow, visually separate from document conversion.

Files begins with two primary actions:

- `Choose files…` — native multi-select picker.
- `Choose collection folder…` — native directory picker for Bruno/OpenCollection folders.

A focused drop target accepts the same file/folder paths. It must use Wails path-resolution rather than browser `File` contents and must be keyboard-skippable.

## State model

The frontend stores only safe planning data and the user’s current decisions:

- source path/name, not source contents;
- detected kind, confidence, content hash, warnings/losses/error;
- collection/environment/folder/request IDs and selection state;
- destination workspace/root, output name, kind override, conflict action;
- apply result rows.

Raw pasted content exists only in the advanced Paste editor and apply request. File contents are re-read by the backend and hash-validated at apply time. No imported secrets appear in diagnostics, notifications, or progress text.

## Preview layout

Use one compact table/list, preserving source order:

| Control | Source | Detected | Contents | Destination | Status |
|---|---|---|---|---|---|
| Select checkbox | Safe filename/path | Kind + override | Collection, environment, folder, request counts | Name + conflict action | Ready, warning, error, stale |

Each valid row expands to a simple hierarchical selector:

- collection
  - environments
  - folders
    - requests

Unchecking a parent updates descendants; a mixed parent uses the indeterminate state. Existing collection folders are whole-collection mounts and explicitly disable child filtering rather than pretending to copy only part of the source.

Errors remain row-scoped. A malformed or unsupported row does not erase valid independent previews. Apply is enabled only when at least one valid selected row remains.

## Detection and override

Automatic detection runs immediately for picker, folder, drop, URL, and Paste sources. The row shows the detected format and confidence. Manual override is an advanced row control and re-previews the source before Apply.

Recognized-but-unsupported types use specific labels and actions, for example:

- `Swagger 2.0 — conversion not available; use OpenAPI 3`
- `WSDL — SOAP import is not available`
- `ZIP — archive import is not available`
- `Yaak export — direct conversion is not available`

They must never display success, create an empty collection, or fall through to a generic parser.

## Conflicts and atomicity

Before Apply, every candidate shows one of:

- **No conflict**
- **Rename** — default safe choice, with deterministic proposed name.
- **Skip**
- **Replace** — explicit destructive choice with confirmation and recoverable backup until persistence succeeds.

All selected valid rows are one commit batch. Planning, cancellation, invalid rows, stale hashes, write failure, rename failure, and final persistence failure leave AppState and disk unchanged. Independent invalid rows may coexist with a successful selected-valid batch.

## Results

After Apply, keep a concise per-source result list:

- imported/opened destination;
- skipped reason;
- failure reason and next action.

Successful collections appear in the sidebar immediately. Relaunch must restore the same collection paths, hierarchy, environments, scripts, auth, and request file paths.

## Accessibility and native behavior

- Native picker buttons have visible text and keyboard focus.
- The drop target is not the sole interaction path.
- Source-mode tabs use a tablist or equivalent button semantics with current-state exposure.
- Preview checkboxes, expanders, kind selectors, conflict selectors, and Apply have programmatic labels.
- Errors are associated with their source rows and summarized in an `aria-live` region without moving focus unexpectedly.
- Escape dismisses frontend dialogs; native picker cancellation returns to the unchanged Import view.
- Focus returns to the invoking button after picker cancellation and to the first preview row after successful selection.
- Small windows keep the action footer reachable and use horizontal containment only inside the preview table, never for the whole page.

## Native acceptance path

At the final package hash, each clean run must visibly exercise:

1. File menu and command-bar entry to the same view.
2. Picker cancellation with no mutation.
3. Single valid file preview/apply/relaunch.
4. Multi-file mixed-validity preview and selective apply.
5. Existing folder open without source mutation.
6. Drag/drop parity with picker.
7. URL success and deterministic error.
8. Paste fallback and manual override.
9. Rename/skip/replace conflicts.
10. A failed atomic apply with no disk or state residue.

