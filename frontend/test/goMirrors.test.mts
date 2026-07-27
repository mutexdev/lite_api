import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { normalizedRunnerIterations, MAX_RUNNER_ITERATIONS } from '../src/lib/preferences.ts'

// Two pieces of frontend logic claim in their comments to mirror Go code.
// Neither claim was checked, and a comment that says "mirrors X" is exactly the
// kind that stays put while X changes.
//
// These read the Go source, the same way test/nativeMenu.test.mts reads
// native_menu.go. Both files are in this repo, so the check costs nothing and
// fails the moment the two drift.

function goSource(relative: string): string {
  return readFileSync(fileURLToPath(new URL(`../../${relative}`, import.meta.url)), 'utf8')
}

function goFunction(source: string, name: string): string {
  const start = source.indexOf(`func ${name}(`)
  assert.ok(start >= 0, `${name} not found in the Go source — the mirror comment names a function that is gone`)
  const end = source.indexOf('\n}\n', start)
  assert.ok(end > start, `could not find the end of ${name}`)
  return source.slice(start, end)
}

// preferences.ts says normalizedRunnerIterations "mirrors runner.NormalizeIterations
// in the Go side, including the 200 cap". The cap is the part worth checking:
// the frontend uses it to stop the input showing a number the backend will not
// honour, so a drift means the UI promises 500 iterations and 200 run.
test('the runner iteration cap matches the Go limit', () => {
  const runner = goFunction(goSource('internal/runner/iterations.go'), 'NormalizeIterations')
  assert.match(runner, /IterationLimit/, 'the Go function no longer clamps to a named limit')

  const limit = /IterationLimit\s*=\s*(\d+)/.exec(goSource('internal/runner/iterations.go'))
    
  assert.ok(limit, 'could not find the value of runner.IterationLimit in the Go source')

  assert.equal(
    MAX_RUNNER_ITERATIONS,
    Number(limit[1]),
    'the frontend cap and the Go limit have drifted; the input would offer a count the backend silently reduces'
  )
  assert.equal(normalizedRunnerIterations(Number(limit[1]) + 1), Number(limit[1]))
})

// requestScanning.ts calls scanBodyPrompts "the frontend twin of
// scanBodyPromptVariables in internal/scripting", and says both "fail the same
// silent way — a mode whose fields go unscanned means the user is never asked,
// and the request goes out with a literal {{?token}} in it".
//
// The claim that can be checked mechanically is that they dispatch over THE
// SAME SET OF BODY MODES. If Go scans a mode the frontend does not, the dialog
// never asks and the token is sent literally.
test('both prompt scanners dispatch over the same body modes', () => {
  const go = goFunction(goSource('internal/scripting/scripting.go'), 'scanBodyPromptVariables')
  const goModes = new Set(
    [...go.matchAll(/case ((?:"[a-zA-Z]+"(?:,\s*)?)+):/g)]
      .flatMap((m) => [...m[1].matchAll(/"([a-zA-Z]+)"/g)].map((x) => x[1]))
  )

  const ts = readFileSync(fileURLToPath(new URL('../src/lib/requestScanning.ts', import.meta.url)), 'utf8')
  const tsFn = ts.slice(ts.indexOf('export function scanBodyPrompts('))
  const tsBody = tsFn.slice(0, tsFn.indexOf('\n}\n'))
  const tsModes = new Set([...tsBody.matchAll(/body\.mode === '([a-zA-Z]+)'/g)].map((m) => m[1]))

  assert.ok(goModes.size >= 7, `only parsed ${goModes.size} Go modes; the parse is wrong`)

  const missingInTS = [...goModes].filter((mode) => !tsModes.has(mode))
  assert.deepEqual(
    missingInTS,
    [],
    'Go scans these modes and the frontend does not — a prompt in one is never asked for'
  )

  const missingInGo = [...tsModes].filter((mode) => !goModes.has(mode))
  assert.deepEqual(
    missingInGo,
    [],
    'the frontend scans these modes and Go does not'
  )
})

// THE ONE PLACE THEY DELIBERATELY DIFFER, pinned so it is a decision rather
// than a drift.
//
// Go skips a multipart part with `!part.Enabled` — false OR unset. The frontend
// skips only `enabled === false`, so a part whose flag is absent IS scanned.
// The frontend therefore prompts for a superset of what Go substitutes, which
// is the safe direction: the user may be asked for a value that goes nowhere,
// but never has a literal token sent because nobody asked.
//
// Flipping either guard reverses that, so the asymmetry is recorded here.
test('the multipart enabled guards differ, and in the safe direction', () => {
  const go = goFunction(goSource('internal/scripting/scripting.go'), 'scanBodyPromptVariables')
  assert.match(go, /if !part\.Enabled \{/, 'the Go guard changed shape')

  const ts = readFileSync(fileURLToPath(new URL('../src/lib/requestScanning.ts', import.meta.url)), 'utf8')
  const tsFn = ts.slice(ts.indexOf('export function scanBodyPrompts('))
  assert.match(
    tsFn.slice(0, tsFn.indexOf('\n}\n')),
    /part\.enabled === false/,
    'the frontend guard changed shape; if it now skips an unset flag as well, a part Go scans could go unprompted'
  )
})
