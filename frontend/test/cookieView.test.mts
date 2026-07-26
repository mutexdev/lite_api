// Presenting the cookie jar.
//
// None of this decides what gets sent — the Go jar does — but all of it decides
// what the user believes is stored. A cookie hidden from the panel is one
// nobody can inspect or delete.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  emptyCookieForm,
  cookieFlags,
  cookieMatches,
  cookieHeaderPreview,
  cookieGroups
} from '../src/lib/cookieView.ts'

const cookie = (o: Record<string, unknown>) => ({ name: 'c', value: 'v', ...o }) as never

// The defaults are the safe reading of an unfinished form.
test('a blank cookie form defaults to the narrower scope', () => {
  const form = emptyCookieForm()
  assert.equal(form.path, '/', 'a cookie with no path is a cookie for nothing')
  assert.equal(form.session, true, 'no expiry means a session cookie')
  assert.equal(form.hostOnly, true, 'the narrower scope is the one to opt out of')
  assert.equal(form.secure, false)
  assert.equal(form.httpOnly, false)
})

// A blank cell reads as "not loaded"; "none" states that the cookie carries no
// protections, which is information rather than its absence.
test('flags say none rather than nothing', () => {
  assert.equal(cookieFlags(cookie({})), 'none')
  assert.equal(cookieFlags(cookie({ secure: true })), 'secure')
  assert.equal(cookieFlags(cookie({ secure: true, httpOnly: true })), 'secure, httpOnly')
  assert.equal(cookieFlags(cookie({ sameSite: 'Lax' })), 'sameSite=Lax')
  assert.equal(
    cookieFlags(cookie({ secure: true, httpOnly: true, sameSite: 'Strict', hostOnly: true })),
    'secure, httpOnly, sameSite=Strict, hostOnly'
  )
})

// "httpOnly" is the query someone runs when auditing a jar; matching only names
// would answer it with nothing.
test('searching matches the flags, not just the name and value', () => {
  const secure = cookie({ name: 'session', secure: true, httpOnly: true })
  assert.equal(cookieMatches(secure, 'httponly'), true)
  assert.equal(cookieMatches(secure, 'secure'), true)
  assert.equal(cookieMatches(cookie({ sameSite: 'Lax' }), 'samesite=lax'), true)
  assert.equal(cookieMatches(cookie({}), 'none'), true, 'an unprotected cookie is findable by its lack of flags')
})

test('searching matches name, value, domain and path', () => {
  const c = cookie({ name: 'sid', value: 'abc123', domain: 'api.test', path: '/admin' })
  for (const query of ['sid', 'abc123', 'api.test', '/admin']) {
    assert.equal(cookieMatches(c, query), true, query)
  }
  assert.equal(cookieMatches(c, 'nomatch'), false)
})

test('the header preview joins as a Cookie header would', () => {
  assert.equal(cookieHeaderPreview([cookie({ name: 'a', value: '1' }), cookie({ name: 'b', value: '2' })]), 'a=1; b=2')
  assert.equal(cookieHeaderPreview([]), '')
})

test('cookies group by domain, sorted', () => {
  const groups = cookieGroups(
    [cookie({ domain: 'z.test' }), cookie({ domain: 'a.test' }), cookie({ domain: 'z.test', name: 'c2' })],
    ''
  )
  assert.deepEqual(groups.map((g) => g.domain), ['a.test', 'z.test'])
  assert.equal(groups[1].cookies.length, 2)
})

// It is still in the jar; hiding it means nobody can delete it.
test('a cookie with no domain is grouped, not dropped', () => {
  const groups = cookieGroups([cookie({ domain: '' })], '')
  assert.deepEqual(groups.map((g) => g.domain), ['(no domain)'])
  assert.equal(groups[0].cookies.length, 1)
})

// Two cookies of the same name on different paths is exactly the confusion this
// panel exists to resolve, so they must sit together and read distinctly.
test('within a domain the order is path then name', () => {
  const groups = cookieGroups(
    [
      cookie({ domain: 'a.test', name: 'sid', path: '/admin' }),
      cookie({ domain: 'a.test', name: 'sid', path: '/' }),
      cookie({ domain: 'a.test', name: 'aaa', path: '/admin' })
    ],
    ''
  )
  assert.deepEqual(
    groups[0].cookies.map((c) => [c.path, c.name]),
    [['/', 'sid'], ['/admin', 'aaa'], ['/admin', 'sid']]
  )
})

test('a cookie with no path sorts as if it were root', () => {
  const groups = cookieGroups([cookie({ domain: 'a.test', name: 'b', path: '/z' }), cookie({ domain: 'a.test', name: 'a' })], '')
  assert.deepEqual(groups[0].cookies.map((c) => c.name), ['a', 'b'])
})

test('a query filters within groups and drops empty ones', () => {
  const groups = cookieGroups(
    [cookie({ domain: 'a.test', name: 'keep' }), cookie({ domain: 'b.test', name: 'drop' })],
    'keep'
  )
  assert.deepEqual(groups.map((g) => g.domain), ['a.test'], 'a group with no matches disappears entirely')
})

test('each group carries its own header preview', () => {
  const groups = cookieGroups(
    [cookie({ domain: 'a.test', name: 'x', value: '1' }), cookie({ domain: 'b.test', name: 'y', value: '2' })],
    ''
  )
  assert.equal(groups[0].header, 'x=1')
  assert.equal(groups[1].header, 'y=2', 'a domain must not advertise another domain cookies')
})

test('an empty or absent jar is not an error', () => {
  assert.deepEqual(cookieGroups([], ''), [])
  assert.deepEqual(cookieGroups(undefined, ''), [])
  assert.deepEqual(cookieGroups(undefined, 'query'), [])
})
