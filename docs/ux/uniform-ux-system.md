# LiteAPI's uniform UX system

Written 2026-08-31, from the ten-part audit in `docs/ux/audit-2026-08-31/`.

## The complaint, and what it actually was

The report was "it looks like a different app in each section." That is a
subjective statement with an objective cause, and the audit found it: **there
were almost no shared UI components.** Every pane hand-rolled its own toolbar,
every table its own row actions, every search its own bar. 113 findings — 12
critical, 45 major — but they are not 113 independent problems. They are a
handful of missing primitives, each absence multiplied across every screen that
needed one.

The measured evidence:

| Symptom | Count |
| --- | --- |
| In-pane search UIs, no two alike | 7 |
| Distinct table/grid surfaces | 24 (3 shared components across 12 call sites; **21 hand-rolled**) |
| `<button>` tags in the app | 416, of which only 108 carry any class |
| Distinct button variant classes | ~18 |
| Distinct font sizes in use | 19 |
| Distinct padding / gap / radius literals | 27 / 19 / 11, *on top of* the tokens |
| Design tokens used but defined nowhere | 10 |
| Empty states rendering with no styling at all | 24 |

The last two rows are the tell. `--warning-border` and `--warning-bg-soft` were
used without fallbacks in five places, which makes the whole CSS declaration
invalid — so the "large response" banner, the JSON-diff changed row and the
editor's search highlight had silently lost their borders and backgrounds in
every theme. `.empty-appState` was a one-character divergence from the styled
`.empty-state`, so 24 empty states rendered as stray unstyled sentences. Neither
throws. Neither shows in a diff. Both look, on screen, exactly like a design
that was never finished — which is precisely the impression being reported.

## The rule

**A user should learn each interaction once.**

Concretely: if two surfaces do the same job, they use the same component. Not a
similar-looking copy — the same file. Where that is impossible, the sameness is
enforced by a test, because this codebase has already proved that a comment
saying "keep these in step" does not keep them in step (`style.css:1909`
literally notes that `--warning-border` "doesn't exist" and three rules below it
go on using it).

## The primitives

All in `frontend/src/lib/ui/`. Every one wraps or extends what was already
there rather than replacing it, so surfaces already doing it right did not move.

| Primitive | Replaces | Rule |
| --- | --- | --- |
| `Icon.svelte` | Unicode glyphs (`⌄`, `×`), literal text (`x`, `^`, `v`, `::`), and one-off inline SVG | One 20×20 grid, 1.7 stroke, `currentColor`, always `aria-hidden` |
| `IconButton.svelte` | `class="icon-button"` wrapping multi-letter text | `label` is required and becomes both the accessible name and the tooltip — one vocabulary, not two |
| `SegmentedControl.svelte` | `<select>` for a frequent choice; hand-rolled pill groups | A real WAI-ARIA radiogroup: one tab stop, arrow keys move selection and focus |
| `PaneToolbar.svelte` | Every hand-rolled pane toolbar | **What am I looking at** on the left, **what can I do to it** on the right as icons, status in a middle slot that is the only part allowed to truncate |
| `FindBar.svelte` | Seven different in-pane searches | One layout, one counter format, one Escape behaviour |

Two things deliberately did **not** become primitives:

- **⌘K global search and ⌘⇧P command palette stay modals.** They search the
  workspace, not the thing on screen. That split is a locked product decision.
- **`Modal.svelte` already existed and all 32 dialogs already used it** — the
  audit's one genuinely good news. The modal work is conformance inside a shell
  that was already right, not a new shell.

## Decisions made, and why

**Body mode is a segmented control, and format is separate from mode.**
The stored model has eight body modes, which puts JSON, XML and text on the same
footing as the choice between raw and multipart — so "how am I sending this" and
"in what syntax" were one question. Postman, Insomnia and Bruno all split them.
`lib/workbench/bodyMode.ts` is that split as a pure mapping over the existing
stored values: nothing on disk changes, no Go changes, every imported collection
keeps working, and `sparql` — which the old picker could display but never
select — becomes reachable again.

**The response body is painted by a small scanner, not a read-only CodeMirror.**
The audit recommended CodeMirror, correctly identifying the risk (two palettes
can drift) but paying too much for it: it would put the deferred eleven-package
editor chunk on the path of every response, bring a second search UI into an app
trying to have one, and leave two independent answers to "how much of this body
is rendered." `lib/workbench/bodyHighlight.ts` instead emits flat segments
carrying the token colour and the search-match flag together, over the same
`--syntax-*` tokens the editor uses. The drift risk is closed by a test that
asserts each token kind is painted with the colour `syntaxHighlight.ts` gives
the equivalent lezer tag. Cheaper, and strictly better guarded.

**⌘F belongs to the editor when the caret is in one.** It was bound globally to
Search Sidebar *and* by CodeMirror to find-in-document, with no guard on either
side, so one keypress opened a floating panel and threw focus to the sidebar.
Resolved in favour of the editor, because a find invoked from inside a document
means "find in this document." Exactly one action is withheld — over-correcting
to "suppress shortcuts while typing" would break ⌘S, ⌘Enter and ⌘W in the place
they are used most.

**Derived tokens over new literals.** The five missing tokens are defined once,
in `:root`, in terms of the family tokens each theme already overrides
(`--warning-bg-soft: var(--warning-bg)`). Custom properties substitute at
computed-value time, so one definition yields thirteen correct answers and there
is no new value for a future theme author to forget.

## What is enforced by test, not by convention

This is the part that makes the system survive contact with the next feature.

| Invariant | Test |
| --- | --- |
| Every token used is defined; none relies on a fallback | `designTokens.test.mts` |
| No token exists only inside a theme variant | `designTokens.test.mts` |
| The five derived tokens stay derived | `designTokens.test.mts` |
| Every `empty-*` class in markup has a rule | `designTokens.test.mts` |
| Response colours match the editor's, tag for tag | `bodyHighlight.test.mts` |
| `--syntax-invalid` remains the only red | `bodyHighlight.test.mts`, `syntaxHighlight.test.mts` |
| Segments always reconstruct the body exactly | `bodyHighlight.test.mts` |
| Every stored body mode round-trips through the picker | `bodyMode.test.mts` |
| A focused editor withholds only the shortcuts it claims | `shortcuts.test.mts` |
| Every Secret checkbox has a masked value field | `secretMasking.test.mts` |

## Sequencing

**Done.** Primitives; the ten token bugs; the four surfaces the user named
(body mode, editor find, response highlighting, response toolbar); secret
masking; the `inherit` auth lie.

**In flight.** Four parallel implementers under strict file ownership — modals
and the window picker; tables and DevTools; preferences; the status/format
vocabulary. Nobody but the owner of `App.svelte` and `style.css` edits those two,
so the wave cannot collide; cross-file needs come back as paste-ready handoffs.

**Not attempted, and why.** The 12,408-line `App.svelte` is the root cause of
several findings and splitting it is its own project — the milestone ledger
already names it a single-owner file. The audit's proposal to collapse 19 font
sizes and 27 paddings into a 6-step scale is right but is a mechanical sweep
that should land on a quiet tree, after this wave, not inside it.
