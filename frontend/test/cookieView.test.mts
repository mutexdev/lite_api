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
  cookieGroups,
  cookieExpiresInput,
  cookieExpiry,
  cookieFormFor
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

// Go marshals a zero time.Time as 0001-01-01T00:00:00Z, so an unset expiry
// arrives as a parseable date almost two millennia ago.
test('a zero-value expiry is treated as absent, not as a date in antiquity', () => {
  assert.equal(cookieExpiresInput(cookie({ expires: '0001-01-01T00:00:00Z' })), '')
  assert.equal(cookieExpiry(cookie({ expires: '0001-01-01T00:00:00Z' })), 'session')
})

test('an unusable expiry never reaches the input', () => {
  assert.equal(cookieExpiresInput(cookie({ expires: '' })), '')
  assert.equal(cookieExpiresInput(cookie({ expires: 'not a date' })), '')
  assert.equal(cookieExpiresInput(cookie({})), '')
})

test('a real expiry round-trips as ISO', () => {
  const iso = cookieExpiresInput(cookie({ expires: '2030-06-01T12:00:00Z' }))
  assert.equal(new Date(iso).getUTCFullYear(), 2030)
})

// "session" is the accurate answer for a cookie with no expiry, not a fallback
// standing in for one.
test('every route to no usable expiry displays as session', () => {
  assert.equal(cookieExpiry(cookie({ session: true, expires: '2030-06-01T12:00:00Z' })), 'session')
  assert.equal(cookieExpiry(cookie({ expires: '' })), 'session')
  assert.equal(cookieExpiry(cookie({ expires: 'rubbish' })), 'session')
  assert.notEqual(cookieExpiry(cookie({ expires: 'rubbish' })), 'Invalid Date')
})

test('a real expiry displays as a local timestamp', () => {
  const shown = cookieExpiry(cookie({ expires: '2030-06-01T12:00:00Z' }))
  assert.notEqual(shown, 'session')
  assert.ok(shown.includes('2030'))
})

test('loading a cookie into the form fills every field', () => {
  const form = cookieFormFor(
    cookie({
      id: 'c1',
      name: 'sid',
      value: 'abc',
      domain: 'api.test',
      path: '/admin',
      expires: '2030-06-01T12:00:00Z',
      session: false,
      secure: true,
      httpOnly: true,
      sameSite: 'Lax',
      hostOnly: false
    })
  )
  assert.deepEqual(
    [form.id, form.name, form.value, form.domain, form.path, form.secure, form.httpOnly, form.sameSite, form.hostOnly],
    ['c1', 'sid', 'abc', 'api.test', '/admin', true, true, 'Lax', false]
  )
  assert.ok(form.expires.startsWith('2030'))
})

// A datetime input showing a date beside a ticked "session" box says two
// contradictory things, and whichever the user changes, the other surprises.
test('a session cookie loads with a blank expiry even when one is stored', () => {
  const form = cookieFormFor(cookie({ session: true, expires: '2030-06-01T12:00:00Z' }))
  assert.equal(form.expires, '')
  assert.equal(form.session, true)
})

test('missing path and sameSite fall back the way the blank form does', () => {
  const form = cookieFormFor(cookie({ path: '', sameSite: '' }))
  assert.equal(form.path, '/', 'matching emptyCookieForm, since a cookie with no path is a cookie for nothing')
  assert.equal(form.sameSite, '')
})
