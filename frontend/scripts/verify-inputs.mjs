// Asserts that the checks about to run have something to check.
//
// WHY. Every frontend gate reports success when its inputs are missing:
//
//   npm test    node --test over a glob that matches nothing exits 0 having
//               run zero tests
//   npm run check   svelte-check with src/ absent still finds 103 files in
//                   node_modules and reports 0 errors — it does not drop to
//                   zero, so a "found nothing" guard would not catch it either
//   npm run lint    eslint . with nothing to lint exits 0
//
// So a moved directory, a renamed folder or a changed glob leaves CI green
// having verified almost nothing. These are FLOORS, not targets: they exist to
// catch a collapse, not to track growth, and they should only be lowered
// deliberately.

import process from 'node:process'
import { existsSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

const FLOORS = {
  testFiles: 25,   // 43 today
  libModules: 30,  // 41 today
}

function countFiles(dir, suffix) {
  if (!existsSync(dir)) return 0
  let total = 0
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) total += countFiles(full, suffix)
    else if (entry.endsWith(suffix)) total += 1
  }
  return total
}

const failures = []

const testFiles = countFiles('test', '.test.mts')
if (testFiles < FLOORS.testFiles) {
  failures.push(`found ${testFiles} test files, expected at least ${FLOORS.testFiles}`)
}

const libModules = countFiles('src/lib', '.ts')
if (libModules < FLOORS.libModules) {
  failures.push(`found ${libModules} modules under src/lib, expected at least ${FLOORS.libModules}`)
}

if (!existsSync('src/App.svelte')) {
  failures.push('src/App.svelte is missing')
}

if (failures.length > 0) {
  console.error('verify-inputs: the checks would run against an incomplete tree.')
  for (const failure of failures) console.error(`  - ${failure}`)
  console.error('')
  console.error('node --test, svelte-check and eslint all report SUCCESS when their')
  console.error('inputs are absent, so this would otherwise be a green CI run that')
  console.error('verified almost nothing. Fix the paths, or lower a floor on purpose.')
  process.exit(1)
}
