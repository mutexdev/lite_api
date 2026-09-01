// The five states a {{variable}} can be in, decided once.
//
// A5-07. The chip had three independent implementations — the URL bar and
// key/value overlays, the "variable inspector" strip under the request tabs,
// and the CodeMirror decoration inside the body/script/test/docs editors — and
// they disagreed about nearly everything they could disagree about:
//
//   * three border radii: 3px, 6px, and a hardcoded 2px in the editor's JS
//     theme, none of them the same token;
//   * two different "valid" backgrounds, --accent-tint and --accent-soft, which
//     are genuinely different colours in every theme;
//   * a border on one of the three;
//   * bold on two of the three;
//   * a secret treatment (dotted underline) in the editor and nowhere else;
//   * a wavy red underline for missing/invalid, in the editor and nowhere else.
//
// So a user who learned "orange pill means resolved" in the URL bar met a
// differently-shaped, differently-shaded pill in the inspector, and a third,
// unbolded, sometimes-underlined thing in the body editor. This is the single
// most direct piece of evidence for "it looks like a different app in each
// section", because variables are the one thing that appears in every section.
//
// ── WHY MISSING IS AMBER AND ONLY INVALID IS RED ────────────────────────────
//
// They are different problems with different fixes. A MISSING variable has a
// perfectly good name and simply has not been defined yet in any scope — the
// user's next move is to define it, and this is a normal state to pass through
// while building a request. An INVALID one cannot ever resolve, because the
// name is not a name the resolver will accept, and no amount of defining will
// help. Painting both red made the common, benign one look like a fault and
// spent the app's only red on it.
//
// Kept pure so the mapping is asserted in a test rather than read off three
// stylesheets.

/** What a chip looks like, and why. */
export type VariableChipState =
  /** Resolves, and is not marked secret. */
  | 'resolved'
  /** Resolves, and is marked secret. The chip shows the reference, never a value. */
  | 'secret'
  /** A usable name that nothing in scope defines yet. */
  | 'missing'
  /** A name the resolver will never accept. */
  | 'invalid'
  /** `{{?name}}` — answered by the user at send time, so it is not missing. */
  | 'prompt'

/** The subset of VariableTooltipInfo this decision needs. */
export interface ChipStateInput {
  found?: boolean
  validName?: boolean
  secret?: boolean
}

/**
 * The state of one chip.
 *
 * Order matters and is the whole content of this function. `validName` is
 * checked first because an invalid name cannot meaningfully be "found", and a
 * prompt is checked before `found` because a prompt that has not been answered
 * yet is the feature working, not a missing variable.
 */
export function variableChipState(info: ChipStateInput | undefined, prompt = false): VariableChipState {
  if (prompt) return 'prompt'
  if (!info) return 'missing'
  if (info.validName === false) return 'invalid'
  if (!info.found) return 'missing'
  return info.secret ? 'secret' : 'resolved'
}

/**
 * The accessible name for a chip.
 *
 * The CodeMirror decoration already built one of these into an `aria-label`;
 * the two DOM chips built none, so a screen reader read `{{token}}` as literal
 * braces with no indication that it was resolved, missing or secret. Same
 * sentence for all three now.
 */
export function variableChipLabel(name: string, state: VariableChipState, scope = ''): string {
  const suffix = scope ? ` from ${scope}` : ''
  switch (state) {
    case 'secret':
      return `{{${name}}} — secret variable${suffix}`
    case 'missing':
      return `{{${name}}} — not defined in any scope`
    case 'invalid':
      return `{{${name}}} — invalid variable name`
    case 'prompt':
      return `{{${name}}} — asked for at send time`
    default:
      return `{{${name}}} — variable${suffix}`
  }
}
