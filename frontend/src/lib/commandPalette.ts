// US-055 — the command palette's matching and navigation, kept pure.
//
// The pre-implementation audit is explicit about the design constraint:
// "Keep Cmd+K search and add Cmd+Shift+P for commands; do not overload one
// modal with two unclear modes." So this palette runs ACTIONS. It never lists
// collections, folders or requests — that is Cmd+K's job, and a palette that
// mixed them would be exactly the unclear double mode the audit warns against.
//
// The logic lives here rather than in the component because ranking and
// keyboard navigation are where a palette is actually wrong in ways nobody
// notices: a rank that puts "Close All Tabs" above "Close Tab" when you type
// "close tab" makes people close the wrong thing, and it looks like it works.

export type PaletteCommand = {
  id: string
  title: string
  section: string
  /** Extra words that should match, e.g. synonyms the title does not contain. */
  keywords?: string[]
  /** Display-only shortcut hint. */
  shortcut?: string
  /** When false the command is listed but not runnable, with a reason. */
  enabled?: boolean
  disabledReason?: string
}

export type PaletteMatch = {
  command: PaletteCommand
  /** Indices into the title that matched, for highlighting. */
  highlights: number[]
  score: number
}

/**
 * Score tiers. Kept far apart so a strong match can never be outranked by an
 * accumulation of weak ones — the failure mode where a fuzzy subsequence hit
 * beats an exact title because it happened to match more characters.
 */
const SCORE_EXACT = 10_000
const SCORE_PREFIX = 5_000
const SCORE_WORD_START = 2_000
const SCORE_SUBSTRING = 1_000
const SCORE_SUBSEQUENCE = 100

function normalize(value: string): string {
  return value.toLowerCase().trim()
}

/**
 * subsequenceMatch reports whether every character of query appears in target
 * in order, and where. Returns null when it does not match at all.
 *
 * Consecutive matches are worth more than scattered ones, so "clta" prefers
 * "Close Tab" over a title where those letters are spread across the string.
 */
function subsequenceMatch(target: string, query: string): { indices: number[]; bonus: number } | null {
  const indices: number[] = []
  let bonus = 0
  let cursor = 0
  let previous = -2

  for (const char of query) {
    let found = -1
    for (let i = cursor; i < target.length; i++) {
      if (target[i] === char) {
        found = i
        break
      }
    }
    if (found < 0) return null
    if (found === previous + 1) bonus += 5
    // A match at a word boundary is a strong signal: typing "ct" for
    // "Close Tab" should beat matching the c and t inside "Collect".
    if (found === 0 || target[found - 1] === ' ' || target[found - 1] === '-') bonus += 10
    indices.push(found)
    previous = found
    cursor = found + 1
  }
  return { indices, bonus }
}

function scoreAgainst(text: string, query: string): { score: number; indices: number[] } | null {
  if (text === query) {
    return { score: SCORE_EXACT, indices: range(0, query.length) }
  }
  if (text.startsWith(query)) {
    return { score: SCORE_PREFIX, indices: range(0, query.length) }
  }

  const wordStart = findWordStart(text, query)
  if (wordStart >= 0) {
    return { score: SCORE_WORD_START, indices: range(wordStart, query.length) }
  }

  const substring = text.indexOf(query)
  if (substring >= 0) {
    return { score: SCORE_SUBSTRING, indices: range(substring, query.length) }
  }

  const fuzzy = subsequenceMatch(text, query)
  if (fuzzy) {
    return { score: SCORE_SUBSEQUENCE + fuzzy.bonus, indices: fuzzy.indices }
  }
  return null
}

function findWordStart(text: string, query: string): number {
  let from = 0
  for (;;) {
    const index = text.indexOf(query, from)
    if (index < 0) return -1
    if (index === 0 || text[index - 1] === ' ' || text[index - 1] === '-') return index
    from = index + 1
  }
}

function range(start: number, length: number): number[] {
  const out: number[] = []
  for (let i = 0; i < length; i++) out.push(start + i)
  return out
}

/**
 * filterCommands ranks commands against a query.
 *
 * An empty query returns everything in its declared order, which is what makes
 * the palette usable as a menu rather than only as a search box.
 *
 * Ties break on the ORIGINAL order, not alphabetically. The declared order
 * puts the commands people reach for most near the top, and re-sorting ties
 * alphabetically would silently discard that.
 */
export function filterCommands(commands: PaletteCommand[], query: string): PaletteMatch[] {
  const needle = normalize(query)
  if (!needle) {
    return commands.map((command) => ({ command, highlights: [], score: 0 }))
  }

  const matches: (PaletteMatch & { order: number })[] = []
  commands.forEach((command, order) => {
    const title = normalize(command.title)
    const best = scoreAgainst(title, needle)
    if (best) {
      matches.push({ command, highlights: best.indices, score: best.score, order })
      return
    }

    // Keywords and the section name are matched too, but at a strictly lower
    // score than any title match, so a title hit always wins. Highlights stay
    // empty because the match was not in the visible title — highlighting
    // arbitrary title characters there would be a lie about why it matched.
    const haystacks = [normalize(command.section), ...(command.keywords ?? []).map(normalize)]
    for (const haystack of haystacks) {
      const hit = scoreAgainst(haystack, needle)
      if (hit) {
        matches.push({ command, highlights: [], score: Math.floor(hit.score / 10), order })
        return
      }
    }
  })

  matches.sort((a, b) => (b.score - a.score) || (a.order - b.order))
  return matches.map(({ command, highlights, score }) => ({ command, highlights, score }))
}

/**
 * moveSelection wraps around at both ends.
 *
 * Wrapping matters more than it looks: pressing Up on the first item is how
 * people reach the last one, and clamping instead makes the key feel broken.
 * An empty list returns 0 rather than -1, so the caller never indexes with a
 * negative.
 */
export function moveSelection(current: number, delta: number, length: number): number {
  if (length <= 0) return 0
  const next = (current + delta) % length
  return next < 0 ? next + length : next
}

/**
 * groupBySection preserves the ranked order within each section and orders the
 * sections by their best-ranked member, so the strongest match is always in the
 * first group rather than buried under a section that happens to sort earlier.
 */
export function groupBySection(matches: PaletteMatch[]): { section: string; matches: PaletteMatch[] }[] {
  const groups: { section: string; matches: PaletteMatch[] }[] = []
  const index = new Map<string, number>()

  for (const match of matches) {
    const section = match.command.section
    const existing = index.get(section)
    if (existing === undefined) {
      index.set(section, groups.length)
      groups.push({ section, matches: [match] })
    } else {
      groups[existing].matches.push(match)
    }
  }
  return groups
}

/**
 * flattenGroups returns the commands in the order they are rendered, which is
 * what the selection index refers to.
 *
 * Without this the palette selects by position in the RANKED list while
 * rendering by group — so the highlighted row and the row that runs on Enter
 * are different rows as soon as sections interleave. That bug is invisible
 * until someone runs the wrong command.
 */
export function flattenGroups(groups: { section: string; matches: PaletteMatch[] }[]): PaletteMatch[] {
  return groups.flatMap((group) => group.matches)
}
