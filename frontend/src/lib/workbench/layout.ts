// Sizing and persistence for the two draggable dividers in the workbench: the
// sidebar's width and the request/response split.
//
// The clamps are the reason this is worth its own module. A width restored from
// disk goes straight into a CSS custom property, and a split fraction goes
// straight into a flex-basis, so a value from a corrupt or hand-edited entry is
// rendered rather than validated. Every path that can set either one — the
// drag, the slider, the restore — goes through the same clamp here.

/** The width the sidebar starts at, and the width double-clicking resets it to. */
export const DEFAULT_SIDEBAR_WIDTH = 312
/** The split the response pane starts at, and the one double-clicking resets to. */
export const DEFAULT_RESPONSE_SPLIT = 0.52

const MIN_SIDEBAR_WIDTH = 220
const MAX_SIDEBAR_WIDTH = 420
const MIN_RESPONSE_SPLIT = 0.3
const MAX_RESPONSE_SPLIT = 0.7

export type WorkbenchLayoutName = 'sidebar-width' | 'response-split'

/**
 * The three widths at which the shell changes shape.
 *
 * WHAT WENT WRONG. Five hardcoded breakpoints — 1180, 960, 800, 680 and 610 —
 * were scattered across `style.css`, `WorkspaceCommandBar.svelte` and
 * `RequestCommandStrip.svelte`, chosen independently of each other. The visible
 * result was a shell that reflowed in stages that did not correspond: between
 * 960 and 1180 the command bar had already dropped its button labels while the
 * layout underneath was still in its wide arrangement, and below 960 the
 * sidebar flipped to an overlay — the single biggest shape change the app makes
 * — while the command bar sat unchanged, waiting for its own 800px step. The
 * chrome and the content it sits above were reflowing on unrelated schedules,
 * which is exactly how one window ends up looking like two applications.
 *
 * These are the numbers `style.css` already used for the SHELL itself, so this
 * constant does not invent a scale — it names the one that was already there
 * and makes the two component files defer to it:
 *
 *   wide     1180  the topbar and the recovery list already stepped here
 *   medium    960  the sidebar becomes an overlay and the workbench stacks
 *   compact   680  the last step before the panes are simply columns
 *
 * CSS cannot read a TypeScript constant, so the two owned components still
 * write the numbers as literals in their `@media` queries. `layout.test.mts`
 * greps those files and fails if a query names a width that is not in this
 * object — which is the same enforcement-by-test the token and body-mode work
 * uses, and the reason the numbers cannot drift apart again silently.
 */
export const SHELL_BREAKPOINTS = {
  wide: 1180,
  medium: 960,
  compact: 680,
} as const

export type ShellBreakpointName = keyof typeof SHELL_BREAKPOINTS

/** The scale as a set of widths, for the media-query audit in the tests. */
export const SHELL_BREAKPOINT_WIDTHS: readonly number[] = Object.values(SHELL_BREAKPOINTS)

/**
 * Rounds to a whole pixel and holds the sidebar between its bounds.
 *
 * The bounds are not cosmetic: below the minimum the request-tree rows truncate
 * to the point of being unreadable, and above the maximum the response pane
 * loses more width than the sidebar gains in use. NaN clamps to the minimum
 * rather than propagating, since `Math.max` returns NaN for it and a NaN width
 * renders the shell with no sidebar at all.
 */
export function clampSidebarWidth(value: number): number {
  if (!Number.isFinite(value)) return MIN_SIDEBAR_WIDTH
  return Math.max(MIN_SIDEBAR_WIDTH, Math.min(MAX_SIDEBAR_WIDTH, Math.round(value)))
}

/**
 * Holds the response split between its bounds.
 *
 * Unlike the width this is deliberately NOT rounded — it is a fraction, and
 * rounding it would collapse every position to 0 or 1.
 */
export function clampResponseSplit(value: number): number {
  if (!Number.isFinite(value)) return MIN_RESPONSE_SPLIT
  return Math.max(MIN_RESPONSE_SPLIT, Math.min(MAX_RESPONSE_SPLIT, value))
}

/**
 * Builds the storage key for one layout value within a window's scope.
 *
 * Returns "" when the scope is unknown. That is the whole point of the empty
 * check at every call site: the scope arrives from an async
 * `GetWebStorageScope()` call, so during the first frames of startup it is "".
 * Falling back to an unscoped key there would make every workspace window read
 * and write the same entry, and the layouts would fight each other on every
 * drag.
 */
export function workbenchStorageKey(scope: string, name: WorkbenchLayoutName): string {
  return scope ? `liteapi.workbench.v3.${scope}.${name}` : ''
}

/** The subset of the Storage interface this module uses. */
export interface LayoutStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

export interface WorkbenchLayout {
  sidebarWidth: number
  responseSplit: number
}

function defaultStorage(): LayoutStorage | null {
  try {
    return globalThis.localStorage ?? null
  } catch {
    // Reading the property itself throws in a WebView with storage disabled.
    return null
  }
}

/**
 * Reads the saved layout, falling back to `current` for anything missing,
 * unparseable or out of range.
 *
 * Takes the current values rather than the defaults so a partially saved layout
 * keeps whatever the user has already dragged this session instead of snapping
 * the untouched divider back to its default.
 */
export function readWorkbenchLayout(
  scope: string,
  current: WorkbenchLayout,
  storage: LayoutStorage | null = defaultStorage(),
): WorkbenchLayout {
  const sidebarKey = workbenchStorageKey(scope, 'sidebar-width')
  const splitKey = workbenchStorageKey(scope, 'response-split')
  if (!sidebarKey || !splitKey || !storage) return current
  try {
    const storedSidebar = storage.getItem(sidebarKey)
    const storedSplit = storage.getItem(splitKey)
    // An absent entry must not read as 0 — Number(null) and Number("") are both
    // 0, which is finite, and would clamp to the minimum instead of being left
    // alone.
    const savedSidebar = storedSidebar === null || storedSidebar === '' ? Number.NaN : Number(storedSidebar)
    const savedSplit = storedSplit === null || storedSplit === '' ? Number.NaN : Number(storedSplit)
    return {
      sidebarWidth: Number.isFinite(savedSidebar) ? clampSidebarWidth(savedSidebar) : current.sidebarWidth,
      responseSplit: Number.isFinite(savedSplit) ? clampResponseSplit(savedSplit) : current.responseSplit,
    }
  } catch {
    // Use what is on screen when storage is unavailable or corrupt.
    return current
  }
}

/**
 * Saves the layout, doing nothing when the scope is unknown or storage refuses.
 *
 * Returns whether anything was written, which is what the tests assert on —
 * a silent no-op and a successful write are otherwise indistinguishable.
 */
export function writeWorkbenchLayout(
  scope: string,
  layout: WorkbenchLayout,
  storage: LayoutStorage | null = defaultStorage(),
): boolean {
  const sidebarKey = workbenchStorageKey(scope, 'sidebar-width')
  const splitKey = workbenchStorageKey(scope, 'response-split')
  if (!sidebarKey || !splitKey || !storage) return false
  try {
    storage.setItem(sidebarKey, String(layout.sidebarWidth))
    storage.setItem(splitKey, String(layout.responseSplit))
    return true
  } catch {
    // Layout persistence is an enhancement; a restrictive WebView storage quota
    // must not block API work.
    return false
  }
}

export interface SplitBounds {
  top: number
  left: number
  width: number
  height: number
}

/**
 * Converts a pointer position into a clamped split fraction.
 *
 * `vertical` selects which axis the panes stack along — the compact layout
 * always stacks them, whatever the persisted wide-layout preference says, so
 * the caller decides rather than this function inferring it.
 *
 * A zero-sized bounds yields the current fraction unchanged instead of dividing
 * by zero: a workbench measured before layout has a height of 0, and NaN there
 * would stick until the next successful drag.
 */
export function splitFractionAt(
  bounds: SplitBounds,
  point: { clientX: number; clientY: number },
  vertical: boolean,
  current: number,
): number {
  const extent = vertical ? bounds.height : bounds.width
  if (!extent) return clampResponseSplit(current)
  const offset = vertical ? point.clientY - bounds.top : point.clientX - bounds.left
  return clampResponseSplit(offset / extent)
}
