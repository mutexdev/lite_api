# Handoff — response inspector, second pass (X2)

Closes A2-02, A2-06 (remainder), A2-07, A2-08, A2-09, A2-11 and A9-12 (this
file's tables only). The first pass had already closed A2-01, A2-03/04/05 and
the headers/metadata tables; nothing landed here regresses them —
`bodyHighlight.test.mts` still passes, the byte-budget story is still told once,
and the response palette is still asserted equal to the editor's tag for tag.

## Verification

From `frontend/`, after the last edit:

| Command | Result |
| --- | --- |
| `npm run check` | 287 files, **0 errors, 0 warnings** |
| `npm run lint` | clean |
| `npm test` | **1334 tests, 1334 pass, 0 fail** |
| `npm run build` | no compile warnings (no unused-CSS, no a11y) |

The final run is fully green. Three failures appeared mid-session and were all
other implementers' in-flight work, since fixed by their owners:
`test/preferencesRows.test.mts` (shared row primitive),
`test/authFields.test.mts` (AuthForm call sites), and a parse error in
`src/lib/unresolvedVariables.ts`. My files were clean in every run; the 31 tests
added here all pass.

## Changes per file

### `frontend/src/lib/workbench/jsonTree.ts` — rewritten (A2-02)

Was a flat list of root keys, each carrying a `JSON.stringify(value, null, 2)`
dump. Now a recursive walk producing a real tree.

- `JsonTreeEntry` gains `path`, `kind`, `summary`, `children`, `childCount`,
  `collapsed`; keeps `name`, `value`, `text`. `text` is now the **leaf's** JSON
  and `''` for an expanded container, so a container renders its children rather
  than a serialisation of itself.
- Four bounds, because they fail four different ways and no one of them catches
  the others: `JSON_TREE_MAX_ENTRIES` (100, now **per container**, not just at
  the root), `JSON_TREE_BUDGET` (96 KB, charged on **leaves only** — charging
  containers double-counts every nested value once per level and stops an
  ordinary 40 KB response expanding past depth two), new `JSON_TREE_MAX_DEPTH`
  (12) and new `JSON_TREE_MAX_NODES` (4,000, the total-DOM-node bound, taking
  its reasoning from `maxHighlightSegments`).
- Cycles are caught by **identity** (an ancestor set, popped on the way out),
  not left to the depth bound — otherwise `a.self = a` renders twelve levels of
  a field that does not exist twelve times. The ancestor set is popped so the
  same object reached by two different paths still renders twice.
- New `countJsonTreeMatches` / `jsonTreeNodeMatches` for the find bar.

### `frontend/src/lib/workbench/JsonTreeView.svelte` — new (A2-02)

- A recursive **snippet** (not a self-importing component) renders the tree.
  `<details>` per container; depth 0 open.
- Every leaf's text goes through `highlightSegments(text, 'json')` — the same
  scanner, therefore the same `--syntax-*` tokens, as the body view. There is no
  second palette here to keep in step, which was the whole point of
  `bodyHighlight.ts` existing instead of a second read-only CodeMirror.
- Search: a query opens the branches containing hits (one DFS, not a per-node
  subtree query — that is quadratic and stutters at 4,000 nodes) and paints hits
  in both names and values.
- A container cut short by a bound says so **on the container**
  ("Showing 100 of 240."), not only in a footnote at the bottom of the panel,
  where a reader cannot tell which branch stopped.

### `frontend/src/lib/workbench/ResponseNotice.svelte` — new (A2-11)

**The rule**, stated in the file:

- **Nothing has happened yet** → the app's shared `.empty-state`. No tone, no
  remedy. ("Ready for a response", "No console output".)
- **Something happened and the body is not what you expected** → `ResponseNotice`
  with a tone. It replaces the body, states the cause as its title, and holds
  whatever action fixes it.
- **The body is fine but partial** → neither. That is the truncation strip under
  the toolbar, which *accompanies* a body rather than replacing one; a card
  there would read as a fault.

Three tones, each a claim about the response rather than about the prose:
`error` = the request produced no body, `warning` = it did and the pane will not
render it, `info` = it did and the pane rendered something else about it.
Tone is carried by border **and** heading colour, not background alone —
background is the first thing lost to a high-contrast setting, and it was the
only signal three of the four containers had.

Replaces `response-empty-state`, `response-tls-warning`, `response-warning` and
`binary-response-card`.

### `frontend/src/lib/workbench/ResponseInspector.svelte`

- **A2-06 remainder.** Timeline and compare moved onto `PaneToolbar`. Timeline:
  `FindBar` + a five-way `SegmentedControl` for the phase filter (counts moved
  into each segment's tooltip), count in the status slot, Copy/Export as
  `IconButton`s. Compare: the target `<select>` stays a select (its options are
  every saved example, so the list is unbounded and a segmented control of
  unbounded width is what `PaneToolbar` exists to keep off the left edge), the
  "show unchanged rows" checkbox becomes a pressed-state `IconButton` on the
  right, and a changed-lines/changed-headers count fills the status slot.
- **A2-07.** The timeline count lost its `aria-live` — it recomputed per
  keystroke of the search box, so typing "oauth" queued five announcements of a
  number still changing. `FindBar` now carries the announcement from its stable
  wrapper. Also removed `aria-live` from the resting empty state: static text in
  a live region re-announced the placeholder every time the user switched
  response tabs on an unsent request. Remaining live regions are all legitimate
  (copy status, byte count — neither is keystroke-driven), and a test now
  enforces that no live region is paired with a search-derived count.
- **A2-08.** `⇧⌘F` / `Ctrl+Shift+F`, on a `<svelte:window>` listener, focuses
  the find bar **of the sub-view you are on** (body, headers or timeline).
  Why not ⌘F: it is owned twice already — Search Sidebar globally, CodeMirror's
  own find inside an editor — and `shortcuts.ts` resolved that in the editor's
  favour deliberately (`editorOwnedShortcutActions`). Binding a third meaning to
  it from a component would undo that decision from the one place nothing in
  `shortcuts.ts` would say so. ⇧⌘F is unclaimed by `keybindings.ts` (which uses
  ⇧⌘W/S/T/P and nothing else with Shift) and by CodeMirror's `searchKeymap`
  (Mod-f, Mod-Alt-f, Mod-g, Mod-d, F3 — never Mod-Shift-f), and it reads
  correctly: ⌘F is "find where the caret is", ⇧⌘F is the wider find. The combo is
  matched through `keyBindingComboFromEvent`, so the one place that decides what
  a keydown *is* stays the one place; a modal on screen suppresses it, exactly as
  `shortcuts.ts` treats `modalOpen`. The shortcut is in the search button's
  label, so tooltip and accessible name both carry it.
- **A2-09.** Every `padding`/`margin`/`gap`/`font-size`/`border-radius` in the
  style block is now a `--space-*` / `--font-size-*` / `--radius-*` token, and a
  test enforces it. Genuine one-off measurements (a pane's `max-height` clamp, a
  table's `min-width`, a grid track) are not on the scale and are not converted.
  `var(--surface-raised, var(--surface-soft))` fallbacks dropped — the token is
  defined now, and `designTokens.test.mts` is explicit that a fallback chain
  hides exactly the failure it guards.
- **A9-12.** `.response-kv-table`, `.compare-section` and `.timeline-detail`
  tables hover with the *same expression* as the DevTools network table
  (`color-mix(in srgb, var(--selected-bg) 55%, transparent)`), asserted equal
  rather than hand-matched. **No** `cursor: pointer` and no selected state: these
  rows do nothing when clicked, and borrowing the affordance without the
  behaviour is a worse lie than no feedback.
- The tree is now built lazily (`jsonTreeMode ? boundedJsonTree(...) : empty`).
  It was built for every JSON response whether or not anyone opened the toggle,
  which was affordable when it was a list of root keys and is not now that it
  walks the document.
- Find bar over the tree: `total` becomes the tree's own match count and the
  bar becomes non-steppable ("12 fields" rather than "3 of 12"). Reporting the
  body's match count while the tree is on screen would name hits nothing on
  screen shows — those offsets are into a document the user is not looking at.
  `showingTree` is written once and used by both the template branch and the
  find bar, so the two cannot disagree (a websocket/gRPC stream with a JSON
  content type takes an earlier branch).

### `frontend/src/lib/workbench/response.ts`, `bodyHighlight.ts`

Unchanged. Nothing in A2-02/06/07/08/09/11 or A9-12 needed them, and both are
load-bearing for tests that already pass.

### Tests

- `frontend/test/jsonTree.test.mts` — extended from 11 to 24. One existing
  assertion **changed**: `an array of objects keeps each element serialised` read
  `JSON.parse(entries[1].text).id === 2`, which was exactly the behaviour A2-02
  reports as the bug; it is now
  `an array of objects expands into child nodes rather than dumping each element`.
  New coverage: recursion depth, leaf JSON text (so the highlighter colours it
  unchanged), container summaries, path uniqueness, the per-container cap, the
  depth bound, the total-node bound, cycle-by-identity, shared-object
  re-rendering, budget-charged-per-leaf, and the three match-count behaviours.
- `frontend/test/responseInspector.test.mts` — new, 18 tests, one per finding,
  in the `readFileSync`-against-source style of `syntaxHighlight.test.mts`.
  Notably: the pane-find combo is asserted to collide with **no** binding in
  `keyBindingSections` and **no** preset override (so adding one later fails
  here rather than on someone's keyboard), and the pixel-literal check parses
  declarations out of the style block with comments stripped.

## Something the audit got wrong, and a bug it missed

**A2-11 undercounts, and the missed one is the interesting one.** `<mark>` — the
element the response body's entire find feature paints with — had **no CSS rule
anywhere**: not in `ResponseInspector.svelte`, not in `style.css`, nowhere. So
every search hit rendered in the browser's default yellow-on-black, which
overrode the syntax colour of whatever it wrapped and was the one thing in the
pane belonging to no theme. `.current-match` had no rule either, so the hit
Previous/Next was moving between looked identical to the twenty that were not:
the counter said "3 of 12" and nothing on screen said which one was 3.

`CodeEditor.svelte:303` makes this worse by asserting it is already fixed:

> The find bar's own highlight. Deliberately the SAME pairing the response pane
> uses for `<mark>` — a warning-tinted hit, an accent-tinted current hit — so a
> user who has searched a response body already knows what the editor is telling
> them.

That comment was written against a pairing that did not exist. This is the same
class of failure `uniform-ux-system.md` already documents at `style.css:1909`: a
comment saying "keep these in step" does not keep them in step. Fixed by taking
the editor's four tokens verbatim, and `responseInspector.test.mts` now asserts
the two rules use the same colour tokens, so the sentence stays true.

**A2-02's recommendation (option a, read-only CodeMirror) remains the wrong
trade**, for the reason `bodyHighlight.ts` already gives, and the tree makes it
sharper: CodeMirror virtualises a *document*, and a tree is not a document. The
node ceiling is the bound that matters here and CodeMirror does not supply it.

**A2-06's suggestion to give Metadata/Trailers a search box was not taken.**
Those lists are typically under ten rows. A find bar over eight rows is chrome
that costs more than it saves; the toolbar shell matches, which was the actual
complaint ("at least the chrome matches").

## Paste-ready, for files I could not edit

### 1. `frontend/src/lib/keybindings.ts` — make ⇧⌘F rebindable *(optional)*

The pane-find shortcut is currently hard-coded in `ResponseInspector.svelte`, so
it is not user-rebindable and does not appear in Preferences → Keybindings. That
is a deliberate stopping point, not an oversight: making it configurable needs a
coordinated change across three files nobody in this wave owns together. If the
owner of `App.svelte` wants it, the whole change is:

```ts
// keybindings.ts, in the `Search` section beside globalSearch/commandPalette
findInPane: { mac: 'command+bind+shift+bind+f', windows: 'ctrl+bind+shift+bind+f', name: 'Find in Pane' },
```

```ts
// shortcuts.ts — add to ShortcutAction and to CONFIGURABLE_ACTIONS.
// NOT to EDITOR_OWNED_ACTIONS: ⇧⌘F is not claimed by CodeMirror, so a focused
// editor has no reason to withhold it.
| 'findInPane'
```

```svelte
<!-- App.svelte, in `shortcut`: route it to the pane rather than handling it -->
{#if action === 'findInPane'} responseFindNonce += 1 {/if}
```

…plus a `findNonce` prop on `<ResponseInspector>`. Ping me and I will do the
component half. Until then the component-local listener works and is tested; if
this lands, delete `findInPaneBinding` and the `<svelte:window>` handler from
`ResponseInspector.svelte` and the two shortcut tests in
`responseInspector.test.mts` will need repointing at `keybindings.ts`.

### 2. `frontend/src/style.css` — promote the `<mark>` rule *(recommended)*

I styled `<mark>` inside `ResponseInspector.svelte` and `JsonTreeView.svelte`,
which is scoped to those two components. Any future surface that paints a search
hit with `<mark>` will get browser-yellow again. Whoever owns `style.css` should
paste this next to the `.response-token-*` block at `style.css:2742` and delete
the two scoped copies:

```css
/*
  The in-pane find highlight, for every surface that paints one.

  `<mark>` had no rule at all until 2026-08-31, so a search hit rendered in the
  browser's yellow-on-black — overriding the syntax colour underneath it and
  belonging to no theme. These are the same four tokens CodeEditor.svelte gives
  `.cm-find-match` / `.cm-find-current`, which is what makes the editor's find
  and a pane's find teach the same thing.
*/
mark {
  border-radius: var(--radius-2);
  background: var(--warning-bg-soft);
  outline: 1px solid var(--warning-border);
  color: inherit;
}

mark.current-match {
  background: var(--accent-soft);
  outline: 1px solid var(--accent);
}
```

If this lands, drop the `.response-body mark` / `.response-body mark.current-match`
rules from `ResponseInspector.svelte` and `.json-tree mark` from
`JsonTreeView.svelte`; `responseInspector.test.mts`'s
`a response search hit is painted with the same tokens as an editor search hit`
reads the component style blocks and would need repointing at `style.css`.

### 3. `frontend/src/style.css` — a resting-state variant *(optional)*

`.empty-state` is a dashed box sized for a table cell. The response pane's
resting placeholder needs it centred with a reading measure, which I added
locally as `.response-resting`. If the tables/status implementer finds a second
caller, promote it:

```css
/* A whole pane with nothing in it yet, as opposed to an empty list inside one. */
.empty-state.resting {
  display: grid;
  place-content: center;
  min-height: 220px;
  gap: var(--space-7);
  text-align: center;
}

.empty-state.resting strong { color: var(--text); font-size: var(--font-size-14); }
.empty-state.resting p { max-width: 360px; margin: 0; line-height: 1.5; }
```
