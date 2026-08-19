// US-034 — tests for the memo helpers.
//
// The risk a memo carries is not a wrong answer, it is a STALE one that looks
// completely normal: the sidebar keeps showing the grouping from before the
// rename and nothing reports a problem. So these tests are mostly about
// invalidation, not about caching.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { memoized, KeyedMemo, type Memo } from '../src/lib/memo.ts'

test('memoized computes once for a repeated key', () => {
  let calls = 0
  let memo: Memo<string, number> = null

  const first = memoized(memo, 'k', () => { calls += 1; return 42 })
  memo = first.memo
  const second = memoized(memo, 'k', () => { calls += 1; return 99 })

  assert.equal(first.value, 42)
  assert.equal(second.value, 42, 'the cached value should be returned')
  assert.equal(calls, 1, 'the compute function should not run twice for one key')
})

test('memoized recomputes when the key changes', () => {
  let calls = 0
  let memo: Memo<string, number> = null

  memo = memoized(memo, 'a', () => { calls += 1; return 1 }).memo
  const second = memoized(memo, 'b', () => { calls += 1; return 2 })

  assert.equal(second.value, 2)
  assert.equal(calls, 2)
})

// The failure this guards is the one that matters: a key that does not change
// when the data does returns the old answer forever.
test('memoized returns a stale value when the key fails to change', () => {
  let memo: Memo<string, string> = null
  memo = memoized(memo, 'same-key', () => 'before the rename').memo
  const after = memoized(memo, 'same-key', () => 'after the rename')

  assert.equal(
    after.value,
    'before the rename',
    'this is the documented hazard: callers must key on something that changes with the data'
  )
})

test('memoized handles falsy and undefined values without recomputing', () => {
  let calls = 0
  let memo: Memo<string, number> = null
  memo = memoized(memo, 'k', () => { calls += 1; return 0 }).memo
  const again = memoized(memo, 'k', () => { calls += 1; return 1 })
  assert.equal(again.value, 0, 'zero is a value, not a cache miss')
  assert.equal(calls, 1)
})

test('memoized starts cold from null', () => {
  let calls = 0
  const result = memoized<string, number>(null, 'k', () => { calls += 1; return 7 })
  assert.equal(result.value, 7)
  assert.equal(calls, 1)
})

test('KeyedMemo computes once per key', () => {
  const memo = new KeyedMemo<number>()
  let calls = 0

  assert.equal(memo.get('a', () => { calls += 1; return 1 }), 1)
  assert.equal(memo.get('a', () => { calls += 1; return 2 }), 1)
  assert.equal(memo.get('b', () => { calls += 1; return 3 }), 3)
  assert.equal(calls, 2)
})

// Unbounded is a leak: collections come and go as workspaces are switched, and
// each entry retains whatever it closed over.
test('KeyedMemo evicts the oldest entry past its limit', () => {
  const memo = new KeyedMemo<number>(3)
  memo.get('a', () => 1)
  memo.get('b', () => 2)
  memo.get('c', () => 3)
  assert.equal(memo.size, 3)

  memo.get('d', () => 4)
  assert.equal(memo.size, 3, 'the map must not grow past its limit')

  // 'a' was the oldest and should have gone; asking again recomputes.
  let recomputed = false
  memo.get('a', () => { recomputed = true; return 1 })
  assert.equal(recomputed, true, 'the evicted key should be recomputed')
})

test('KeyedMemo keeps a value of zero rather than treating it as a miss', () => {
  const memo = new KeyedMemo<number>()
  let calls = 0
  memo.get('k', () => { calls += 1; return 0 })
  memo.get('k', () => { calls += 1; return 1 })
  assert.equal(calls, 1, 'zero must be cached like any other value')
})

test('KeyedMemo clear drops everything', () => {
  const memo = new KeyedMemo<number>()
  memo.get('a', () => 1)
  memo.get('b', () => 2)
  assert.equal(memo.size, 2)
  memo.clear()
  assert.equal(memo.size, 0)

  let recomputed = false
  memo.get('a', () => { recomputed = true; return 1 })
  assert.equal(recomputed, true)
})

// A composite key is only as good as the fields in it. This documents the
// contract callers have to meet, using the shape they actually use.
test('a composite key invalidates on any of its parts', () => {
  const key = (collectionID: string, revision: number, query: string) => `${collectionID}:${revision}:${query}`

  let calls = 0
  const memo = new KeyedMemo<string>()
  const compute = () => { calls += 1; return `result-${calls}` }

  memo.get(key('c1', 1, ''), compute)
  memo.get(key('c1', 1, ''), compute)
  assert.equal(calls, 1, 'identical inputs hit the cache')

  memo.get(key('c1', 2, ''), compute)
  assert.equal(calls, 2, 'a bumped revision must miss')

  memo.get(key('c1', 2, 'search'), compute)
  assert.equal(calls, 3, 'a different query must miss')

  memo.get(key('c2', 2, 'search'), compute)
  assert.equal(calls, 4, 'a different collection must miss')
})
