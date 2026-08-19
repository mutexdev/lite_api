// The editor's syntax colours.
//
// The editor shipped with CodeMirror's `defaultHighlightStyle`, which paints
// string literals `#a11` — a dark red within a few points of this app's own
// `--danger` (#8e1a10). So every string value in a JSON body read as an error,
// and in a request editor that is a genuinely bad thing to say by accident:
// the response pane, the linter gutter and the variable underlines all use red
// to mean "this is wrong". A body full of ordinary values looked like a body
// full of problems.
//
// Red strings are not a CodeMirror invention — they come from Visual Studio,
// and VS Code Light (#a31515) and Chrome DevTools (#c41a16) both still do it.
// But the other lineage does not: JetBrains, Atom/One Dark, Nord and
// Catppuccin all paint strings GREEN and reserve red for keywords or errors.
// This app already ships Nord, Catppuccin and VS Code theme variants, so the
// question was never "which palette is correct" — it was "whose palette is
// this line of code in". Hence:
//
//   THE COLOURS ARE NOT IN THIS FILE. Every one is `var(--syntax-*)`, defined
//   per theme in style.css, so Nord gets Nord's green and Catppuccin gets
//   Catppuccin's mauve. A literal colour here would be wrong in eleven themes
//   to be right in one.
//
// The rule the default themes follow, and which syntaxHighlight.test.mts
// enforces: `--syntax-invalid` is the ONLY red. Everything a well-formed
// document contains is painted in something that does not mean "broken".

import { HighlightStyle } from '@codemirror/language'
import { tags } from '@lezer/highlight'

/**
 * The tag → token mapping.
 *
 * Grouped by what a reader is actually looking at rather than by lezer's tag
 * hierarchy, because the hierarchy splits things that should look identical
 * (`tags.bool` and `tags.null` are unrelated tags describing the same column of
 * a JSON document) and joins things that should not.
 *
 * Order matters: lezer resolves a tag against the FIRST matching rule, and the
 * specific tags have to precede the general ones they inherit from. `propertyName`
 * before `name` is the one that decides whether JSON keys are distinguishable
 * from JSON values at all.
 */
export const liteApiHighlightStyle = HighlightStyle.define([
  // JSON keys, XML attribute names — the left-hand side of a document.
  { tag: [tags.propertyName, tags.attributeName], color: 'var(--syntax-key)' },

  // The right-hand side. This is the line the whole change is about.
  { tag: [tags.string, tags.special(tags.string), tags.attributeValue], color: 'var(--syntax-string)' },
  { tag: [tags.number, tags.integer, tags.float], color: 'var(--syntax-number)' },
  { tag: [tags.bool, tags.null, tags.atom, tags.constant(tags.name)], color: 'var(--syntax-boolean)' },

  // Structure.
  { tag: [tags.keyword, tags.modifier, tags.operatorKeyword, tags.controlKeyword], color: 'var(--syntax-keyword)' },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName), tags.macroName], color: 'var(--syntax-function)' },
  { tag: [tags.className, tags.typeName, tags.namespace], color: 'var(--syntax-type)' },
  { tag: [tags.tagName, tags.angleBracket], color: 'var(--syntax-tag)' },
  { tag: [tags.variableName, tags.definition(tags.variableName)], color: 'var(--syntax-variable)' },
  { tag: [tags.operator, tags.punctuation, tags.separator, tags.bracket, tags.derefOperator], color: 'var(--syntax-punctuation)' },
  { tag: [tags.meta, tags.processingInstruction, tags.documentMeta], color: 'var(--syntax-meta)' },

  // Prose. Markdown is a supported language here, and its emphasis marks are
  // the one place where weight and slant carry meaning that colour cannot.
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: 'var(--syntax-comment)', fontStyle: 'italic' },
  { tag: [tags.heading], color: 'var(--syntax-heading)', fontWeight: '600' },
  { tag: [tags.link, tags.url], color: 'var(--syntax-link)', textDecoration: 'underline' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.strong, fontWeight: '600' },
  { tag: tags.strikethrough, textDecoration: 'line-through' },
  { tag: [tags.monospace], color: 'var(--syntax-string)' },

  // The only red, and the reason there is only one: red has to keep meaning
  // "something is wrong here" for the linter's own marks to read as urgent.
  { tag: tags.invalid, color: 'var(--syntax-invalid)' },
])

/**
 * Every custom property the style above depends on.
 *
 * Exported so the tests can assert that each one is defined in each theme
 * rather than silently resolving to nothing — an undefined custom property in a
 * `color:` declaration makes the whole declaration invalid, and the text falls
 * back to the body colour. That failure is invisible in a diff and looks, on
 * screen, exactly like "this language has no grammar yet".
 */
export const syntaxTokenNames = [
  '--syntax-key',
  '--syntax-string',
  '--syntax-number',
  '--syntax-boolean',
  '--syntax-keyword',
  '--syntax-function',
  '--syntax-type',
  '--syntax-tag',
  '--syntax-variable',
  '--syntax-punctuation',
  '--syntax-meta',
  '--syntax-comment',
  '--syntax-heading',
  '--syntax-link',
  '--syntax-invalid',
] as const
