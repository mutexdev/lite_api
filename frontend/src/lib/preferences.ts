// Normalizers for every persisted preference that is read back from disk.
//
// What these share is not a feature but a threat: each value arrives from a
// settings file the app wrote in some earlier version, and each one is used
// without further checking — as a CSS zoom, as a `setTimeout` period, as an
// `http.Client` timeout. A value that survives normalization unchanged is one
// the rest of the app treats as valid.
//
// The interesting part is that the guards are deliberately NOT uniform. Zero
// means "no timeout" for a request and "use the default" for an autosave
// interval; the same input has to normalize two different ways, and collapsing
// them into one shared helper is exactly the mistake this module exists to make
// visible.

export const ZOOM_DEFAULT_PERCENTAGE = 100
export const ZOOM_MIN_PERCENTAGE = 50
export const ZOOM_MAX_PERCENTAGE = 150

export const DEFAULT_CODE_FONT = 'default'
export const DEFAULT_CODE_FONT_SIZE = 13
const MIN_CODE_FONT_SIZE = 1
const MAX_CODE_FONT_SIZE = 32

export const DEFAULT_AUTO_SAVE_INTERVAL_MS = 1000
/**
 * The shortest autosave period accepted.
 *
 * A stored 1 would be honoured as one write per millisecond, which is a
 * continuous rewrite of the collection on disk for as long as the app is open.
 */
const MIN_AUTO_SAVE_INTERVAL_MS = 500

export const MAX_RUNNER_DELAY_MS = 600_000
export const MAX_RUNNER_ITERATIONS = 200

export type ResponsePaneOrientation = 'horizontal' | 'vertical'
export type ThemeMode = 'system' | 'light' | 'dark'

/** Turns an arbitrary stored value into a finite number, or NaN. */
function finite(value: unknown): number {
  const numeric = Number(value)
  return Number.isFinite(numeric) ? numeric : Number.NaN
}

/**
 * Maps a stored request type onto one the app can open, or "".
 *
 * "ws" is the name an earlier build persisted for WebSocket presets; dropping
 * it would silently turn those saved presets into no-ops.
 */
export function normalizePresetRequestType(value: string | undefined): string {
  if (value === 'ws') return 'websocket'
  if (value === 'http' || value === 'graphql' || value === 'grpc' || value === 'websocket') return value
  return ''
}

export function normalizedResponsePaneOrientation(value: string | undefined): ResponsePaneOrientation {
  return value === 'vertical' ? 'vertical' : 'horizontal'
}

export function normalizedThemeMode(value: string | undefined): ThemeMode {
  return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
}

/**
 * Maps a stored theme id onto one that is installed, falling back to the first.
 *
 * A variant id that no longer exists — a theme removed between versions — would
 * otherwise reach `dataset.themeVariant` and match no stylesheet, leaving the
 * app rendered with no theme colours at all.
 */
export function normalizedThemeVariant<T extends { id: string }>(
  value: string | undefined,
  variants: T[],
): string {
  if (variants.length === 0) return ''
  return variants.some((variant) => variant.id === value) ? (value as string) : variants[0].id
}

/**
 * Clamps the UI zoom.
 *
 * A zoom of 0 would scale the whole document to nothing, so it is treated as
 * absent rather than clamped up to the minimum — the user asked for no zoom
 * setting, not for the smallest one.
 */
export function normalizedZoomPercentage(value: number | undefined): number {
  const numeric = Number.isFinite(value) ? Number(value) : ZOOM_DEFAULT_PERCENTAGE
  return Math.min(
    Math.max(numeric || ZOOM_DEFAULT_PERCENTAGE, ZOOM_MIN_PERCENTAGE),
    ZOOM_MAX_PERCENTAGE,
  )
}

/** An empty or whitespace-only font name falls back rather than being applied. */
export function normalizedCodeFont(value: string | undefined): string {
  return value?.trim() || DEFAULT_CODE_FONT
}

/**
 * Clamps the editor font size.
 *
 * 0 is treated as unset rather than clamped to the minimum: a 1px editor is
 * unreadable, and a stored 0 far more likely means "never configured" than
 * "as small as possible".
 */
export function normalizedCodeFontSize(value: number | undefined): number {
  const numeric = finite(value)
  if (Number.isNaN(numeric) || numeric === 0) return DEFAULT_CODE_FONT_SIZE
  return Math.min(Math.max(numeric, MIN_CODE_FONT_SIZE), MAX_CODE_FONT_SIZE)
}

/**
 * Normalizes the autosave period.
 *
 * Zero and negatives are UNSET here — the setting has no "off" value, and a
 * non-positive period would either fire continuously or never. Anything
 * positive is then floored at the minimum, so a stored 1 becomes 500 rather
 * than rewriting the collection a thousand times a second.
 *
 * Note this differs from the request timeout below, where 0 is a legitimate
 * value the user can choose. Same input, two correct answers.
 */
export function normalizedAutoSaveInterval(value: number | undefined): number {
  const numeric = finite(value)
  if (Number.isNaN(numeric) || numeric <= 0) return DEFAULT_AUTO_SAVE_INTERVAL_MS
  return Math.max(Math.round(numeric), MIN_AUTO_SAVE_INTERVAL_MS)
}

/**
 * Normalizes the per-request timeout in milliseconds.
 *
 * ZERO IS KEPT. It is how the user asks for no timeout at all, and the backend
 * reads it that way. Sending the default instead would cap long-running
 * requests the user deliberately left uncapped.
 *
 * Negatives are the ones that must not pass: a negative timeout reaches Go's
 * `http.Client.Timeout`, where it makes every request fail instantly.
 */
export function normalizedRequestTimeout(value: number | undefined): number {
  const numeric = finite(value)
  if (Number.isNaN(numeric) || numeric < 0) return 0
  return Math.round(numeric)
}

/**
 * Clamps the runner's inter-request delay.
 *
 * The ten-minute ceiling is what stops a mistyped delay from producing a run
 * that appears hung with no way to tell it from a stuck request.
 */
export function normalizedRunnerDelayMs(value: number): number {
  const delay = Math.floor(Number(value) || 0)
  if (delay < 0) return 0
  if (delay > MAX_RUNNER_DELAY_MS) return MAX_RUNNER_DELAY_MS
  return delay
}

/**
 * Clamps the runner's iteration count.
 *
 * Mirrors normalizeRunnerIterations in the Go side, including the 200 cap. The
 * backend normalizes again — this only keeps the input from displaying a value
 * the backend will not honour.
 */
export function normalizedRunnerIterations(value: number): number {
  const iterations = Math.floor(Number(value) || 0)
  if (iterations < 1) return 1
  if (iterations > MAX_RUNNER_ITERATIONS) return MAX_RUNNER_ITERATIONS
  return iterations
}

export const DEFAULT_DEVTOOLS_DETAILS_WIDTH = 400
export const DEFAULT_DEVTOOLS_DRAWER_HEIGHT = 320

/** Keeps the DevTools details panel wide enough to read and narrow enough to leave the log visible. */
export function normalizedDevToolsDetailsPanelWidth(value: number | undefined): number {
  const width = Math.round(Number(value) || DEFAULT_DEVTOOLS_DETAILS_WIDTH)
  return Math.max(280, Math.min(800, width))
}

/** Same, for the drawer's height. */
export function normalizedDevToolsDrawerHeight(value: number | undefined): number {
  const height = Math.round(Number(value) || DEFAULT_DEVTOOLS_DRAWER_HEIGHT)
  return Math.max(220, Math.min(720, height))
}

/**
 * Maps a stored tab id onto one that exists, falling back to the first.
 *
 * A tab id removed between versions would otherwise leave the drawer open with
 * no panel rendered inside it.
 */
export function normalizedTabID<T extends string>(
  value: string | undefined,
  tabs: readonly { id: T }[],
  fallback: T,
): T {
  return tabs.some((tab) => tab.id === value) ? (value as T) : fallback
}
