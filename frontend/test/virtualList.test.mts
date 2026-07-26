// US-032 — tests for the virtual list windowing.
//
// Every failure mode here is arithmetic that renders as a visual glitch, so the
// tests assert the INVARIANTS rather than specific indices wherever possible.
// A test pinning "startIndex is 12" locks in today's overscan; a test asserting
// the total height is conserved catches the class of bug that makes the
// scrollbar drift under the cursor.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { computeWindow, keepIndexVisible } from '../src/lib/virtualList.ts'

const ROW = 24
const VIEWPORT = 480

// THE load-bearing invariant. If the total height is not conserved, the scroll
// height changes as the user scrolls and the thumb moves under their cursor.
test('total rendered height is conserved at every scroll position', () => {
  const total = 500
  for (let scrollTop = 0; scrollTop <= total * ROW; scrollTop += 37) {
    const w = computeWindow({ total, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop })
    const rendered = (w.endIndex - w.startIndex) * ROW
    assert.equal(
      w.topPadding + rendered + w.bottomPadding,
      total * ROW,
      `height not conserved at scrollTop ${scrollTop}`
    )
  }
})

test('the window always covers the visible range', () => {
  const total = 500
  for (let scrollTop = 0; scrollTop <= total * ROW; scrollTop += 53) {
    const w = computeWindow({ total, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop })
    const clamped = Math.min(scrollTop, Math.max(0, total * ROW - VIEWPORT))
    const firstVisible = Math.floor(clamped / ROW)
    const lastVisible = Math.min(total - 1, Math.floor((clamped + VIEWPORT - 1) / ROW))

    assert.ok(w.startIndex <= firstVisible, `startIndex ${w.startIndex} misses first visible ${firstVisible}`)
    assert.ok(w.endIndex > lastVisible, `endIndex ${w.endIndex} misses last visible ${lastVisible}`)
  }
})

// The bug a filter produces: the row count shrinks while scrollTop stays where
// it was, pointing past the end of the new list.
test('a scrollTop left over from a longer list still renders rows', () => {
  const w = computeWindow({ total: 5, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 9000 })
  assert.equal(w.startIndex, 0, 'the window should clamp back to the start')
  assert.equal(w.endIndex, 5, 'every remaining row should render')
  assert.equal(w.topPadding, 0)
  assert.equal(w.bottomPadding, 0)
})

test('padding is never negative', () => {
  for (const total of [0, 1, 3, 500]) {
    for (const scrollTop of [-100, 0, 1, 99999]) {
      const w = computeWindow({ total, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop })
      assert.ok(w.topPadding >= 0, `negative topPadding for total ${total} at ${scrollTop}`)
      assert.ok(w.bottomPadding >= 0, `negative bottomPadding for total ${total} at ${scrollTop}`)
    }
  }
})

test('indices stay inside the list at both ends', () => {
  const total = 40
  for (let scrollTop = -500; scrollTop <= total * ROW + 500; scrollTop += 17) {
    const w = computeWindow({ total, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop })
    assert.ok(w.startIndex >= 0, `startIndex ${w.startIndex}`)
    assert.ok(w.endIndex <= total, `endIndex ${w.endIndex} past total ${total}`)
    assert.ok(w.startIndex <= w.endIndex, 'startIndex must not pass endIndex')
  }
})

// A measured row height of zero is reachable: the height comes from the DOM,
// and a hidden or not-yet-laid-out table measures zero. Dividing by it gives
// Infinity indices and an {#each} over an infinite range.
test('a zero or negative row height renders nothing instead of dividing by zero', () => {
  for (const rowHeight of [0, -24]) {
    const w = computeWindow({ total: 500, rowHeight, viewportHeight: VIEWPORT, scrollTop: 100 })
    assert.deepEqual(w, { startIndex: 0, endIndex: 0, topPadding: 0, bottomPadding: 0 })
  }
})

test('an empty list renders nothing and asks for no spacer', () => {
  const w = computeWindow({ total: 0, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 0 })
  assert.deepEqual(w, { startIndex: 0, endIndex: 0, topPadding: 0, bottomPadding: 0 })
})

test('a list shorter than the viewport renders entirely with no padding', () => {
  const w = computeWindow({ total: 3, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 0 })
  assert.equal(w.startIndex, 0)
  assert.equal(w.endIndex, 3)
  assert.equal(w.topPadding, 0)
  assert.equal(w.bottomPadding, 0)
})

// The point of the story: a 500-row list must not put 500 rows in the DOM.
test('a 500-row list renders a bounded window, not the whole list', () => {
  const w = computeWindow({ total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 0 })
  const rendered = w.endIndex - w.startIndex
  const fitInViewport = Math.ceil(VIEWPORT / ROW)
  assert.ok(rendered < 60, `${rendered} rows rendered for a 500-row list`)
  assert.ok(rendered >= fitInViewport, `${rendered} rows is not enough to fill a ${VIEWPORT}px viewport`)
})

test('a fractional scroll offset still covers the partly visible first and last rows', () => {
  // Scrolled half a row: the row above and the row below the viewport edge are
  // both partly on screen and must both be rendered.
  const w = computeWindow({ total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: ROW * 10 + ROW / 2, overscan: 0 })
  assert.ok(w.startIndex <= 10, 'the partly visible top row must be included')
  const lastVisible = Math.floor((ROW * 10 + ROW / 2 + VIEWPORT - 1) / ROW)
  assert.ok(w.endIndex > lastVisible, 'the partly visible bottom row must be included')
})

test('overscan widens the window without breaking the invariant', () => {
  const base = computeWindow({ total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 2400, overscan: 0 })
  const wide = computeWindow({ total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 2400, overscan: 10 })

  assert.ok(wide.startIndex <= base.startIndex)
  assert.ok(wide.endIndex >= base.endIndex)
  assert.equal(wide.topPadding + (wide.endIndex - wide.startIndex) * ROW + wide.bottomPadding, 500 * ROW)
})

test('keepIndexVisible leaves the scroll alone when the row is already on screen', () => {
  const scrollTop = 240
  const unchanged = keepIndexVisible(12, { total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop })
  assert.equal(unchanged, scrollTop, 'an on-screen row must not yank the list')
})

test('keepIndexVisible scrolls up to a row above the viewport', () => {
  const result = keepIndexVisible(2, { total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 1000 })
  assert.equal(result, 2 * ROW, 'the row should end up at the top edge')
})

test('keepIndexVisible scrolls down just enough to reveal a row below', () => {
  const index = 40
  const result = keepIndexVisible(index, { total: 500, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 0 })
  // Just enough: the row sits at the bottom edge rather than the top, so a
  // down-arrow walk moves one row at a time instead of jumping a page.
  assert.equal(result, (index + 1) * ROW - VIEWPORT)
})

test('keepIndexVisible clamps an out-of-range index instead of scrolling past the end', () => {
  const total = 10
  const result = keepIndexVisible(999, { total, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 0 })
  assert.ok(result >= 0)
  assert.ok(result <= total * ROW, 'must not scroll past the content')
  assert.equal(keepIndexVisible(-5, { total, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 100 }), 0)
})

test('keepIndexVisible is inert for an empty list or an unmeasured row height', () => {
  assert.equal(keepIndexVisible(3, { total: 0, rowHeight: ROW, viewportHeight: VIEWPORT, scrollTop: 55 }), 55)
  assert.equal(keepIndexVisible(3, { total: 10, rowHeight: 0, viewportHeight: VIEWPORT, scrollTop: 55 }), 55)
})
