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
