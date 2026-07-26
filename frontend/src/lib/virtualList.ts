// US-032 — the windowing arithmetic behind a virtualised list.
//
// Kept separate from the component because every way this goes wrong is
// arithmetic, and every one of them looks like a rendering glitch rather than a
// calculation error:
//
//   * padding that does not exactly match the skipped rows changes the
//     scrollbar's length, so the thumb drifts under the cursor while dragging
//   * a window one row short leaves a gap at the bottom edge that only appears
//     at certain viewport heights
//   * a scrollTop left over from a longer list — which is what happens the
//     instant a filter is applied — points past the end and renders nothing at
//     all, on a table that visibly still has rows
//
// None of those produce an error anywhere. They produce a table that looks
// broken and a stack trace that never happens.

export type VirtualWindow = {
  /** First row index to render, inclusive. */
  startIndex: number
  /** Last row index to render, exclusive. */
  endIndex: number
  /** Pixels of spacer above the rendered rows. */
  topPadding: number
  /** Pixels of spacer below them. */
  bottomPadding: number
}

export type VirtualWindowInput = {
  total: number
  rowHeight: number
  viewportHeight: number
  scrollTop: number
  /** Extra rows rendered beyond the viewport at each end. */
  overscan?: number
}

/**
 * computeWindow returns which rows to render and how much spacer to put around
 * them.
 *
 * The invariant that matters, and that the tests assert directly:
 *
 *     topPadding + (endIndex - startIndex) * rowHeight + bottomPadding
 *       === total * rowHeight
 *
 * If that ever fails to hold the scroll height changes as the user scrolls, and
 * the scrollbar moves under their cursor.
 */
export function computeWindow({
  total,
  rowHeight,
  viewportHeight,
  scrollTop,
  overscan = 6
}: VirtualWindowInput): VirtualWindow {
  // A non-positive row height would divide by zero and produce Infinity
  // indices. It is reachable: the height is measured from the DOM, and a
  // hidden or not-yet-laid-out table measures zero.
  if (total <= 0 || rowHeight <= 0) {
    return { startIndex: 0, endIndex: 0, topPadding: 0, bottomPadding: 0 }
  }

  const contentHeight = total * rowHeight
  // Clamped against the CONTENT, not just at zero. A scrollTop left from a
  // longer list — exactly what a filter produces — would otherwise put the
  // window past the end and render nothing while rows are plainly visible.
  const maxScroll = Math.max(0, contentHeight - Math.max(0, viewportHeight))
  const clampedScroll = Math.min(Math.max(0, scrollTop), maxScroll)

  const firstVisible = Math.floor(clampedScroll / rowHeight)
  // ceil, not floor: a viewport 2.5 rows tall shows three, and flooring leaves
  // a sliver of blank at the bottom edge that appears only at some heights.
  // The +1 covers a window scrolled to a fractional offset, where the first and
  // last rows are each partly visible.
  const visibleCount = Math.ceil(Math.max(0, viewportHeight) / rowHeight) + 1

  const startIndex = Math.max(0, firstVisible - overscan)
  const endIndex = Math.min(total, firstVisible + visibleCount + overscan)

  return {
    startIndex,
    endIndex,
    topPadding: startIndex * rowHeight,
    bottomPadding: Math.max(0, (total - endIndex) * rowHeight)
  }
}

/**
 * keepIndexVisible returns the scrollTop needed to bring an index into view,
 * or the current scrollTop when it already is.
 *
 * Used by keyboard navigation. Scrolling to the row's exact offset every time
 * would yank the list to put the selection at the top even when it was already
 * comfortably on screen.
 */
export function keepIndexVisible(
  index: number,
  { rowHeight, viewportHeight, scrollTop, total }: Omit<VirtualWindowInput, 'overscan'>
): number {
  if (rowHeight <= 0 || total <= 0) return scrollTop
  const clampedIndex = Math.min(Math.max(0, index), total - 1)
  const rowTop = clampedIndex * rowHeight
  const rowBottom = rowTop + rowHeight

  if (rowTop < scrollTop) return rowTop
  if (rowBottom > scrollTop + viewportHeight) return Math.max(0, rowBottom - viewportHeight)
  return scrollTop
}

export type GroupWindow = {
  start: number
  end: number
  padTop: number
  padBottom: number
}

/**
 * sidebarGroupWindow is computeWindow for ONE GROUP inside a single scrolling
 * list.
 *
 * The sidebar is not one flat list: each collection and folder renders its own
 * {#each}, but they share one scroll container. So a group needs to know how
 * many rows precede it — `offset` — to translate the container's scrollTop into
 * its own coordinate space.
 *
 * IT DIFFERS FROM computeWindow IN ONE WAY THAT MATTERS, and the difference is
 * deliberate rather than an oversight to fix blindly. computeWindow clamps
 * scrollTop against the content height so a stale scroll position — exactly
 * what filtering a longer list leaves behind — cannot put the window past the
 * end. This cannot do the same, because a group legitimately sits outside the
 * viewport: clamping per group would drag every off-screen group's window back
 * to its own content and render rows nobody can see.
 *
 * Instead the window is clamped to [0, count] after the offset is applied,
 * which for a group above the viewport yields start === end === count and for
 * one below yields start === end === 0. Both render nothing, which is right.
 * The scroll container's own height keeps the aggregate honest.
 */
export function sidebarGroupWindow(
  count: number,
  offset: number,
  rowHeight: number,
  viewportHeight: number,
  scrollTop: number,
  overscan = 8
): GroupWindow {
  if (viewportHeight <= 0 || rowHeight <= 0 || count === 0) {
    // Before layout the row height measures zero. Rendering everything is the
    // safe fallback: a virtualised list that renders nothing looks broken,
    // while one that renders too much is merely slow for one frame.
    return { start: 0, end: count, padTop: 0, padBottom: 0 }
  }

  const firstVisible = Math.floor(scrollTop / rowHeight) - offset - overscan
  const lastVisible = Math.ceil((scrollTop + viewportHeight) / rowHeight) - offset + overscan
  const start = Math.max(0, Math.min(count, firstVisible))
  const end = Math.max(start, Math.min(count, lastVisible))

  return {
    start,
    end,
    padTop: start * rowHeight,
    padBottom: (count - end) * rowHeight
  }
}
