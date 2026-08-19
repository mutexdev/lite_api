// Finds the top-level functions in a .svelte <script> that are PURE with
// respect to component state, and can therefore move to a plain .ts module
// without changing behaviour.
//
// Guessing from a function's name does not work. The Go half of this repo
// produced four separate cases where a file's name and size both said "one
// concern" and the actual declaration list said otherwise, so the rule here is
// the same: decide from what the code references, not from what it is called.
//
// The analysis is deliberately CONSERVATIVE. A function is a candidate only if
// no identifier anywhere inside it — including its own locals — collides with a
// piece of component state or a Wails binding. That over-rejects when a local
// happens to shadow a state name, which is the harmless direction: the cost is
// a function left behind, not a function moved that should not have been.
//
//   node scripts/scanScript.mjs                 # summary
//   node scripts/scanScript.mjs --list          # every pure function, by size
//   node scripts/scanScript.mjs --why <name>    # what disqualifies one function

import process from 'node:process'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const SOURCE = 'src/App.svelte'

/** The <script> body, plus the line it starts on so reported lines are real. */
export function scriptBody(text) {
  const open = text.indexOf('<script')
  const start = text.indexOf('>', open) + 1
  const end = text.indexOf('</script>', start)
  if (open < 0 || end < 0) throw new Error(`no <script> block in ${SOURCE}`)
  return { code: text.slice(start, end), offset: text.slice(0, start).split('\n').length }
}

const RUNES = new Set(['$state', '$derived', '$props', '$bindable'])

/** Every identifier appearing anywhere under a node, in any role. */
function identifiersIn(node) {
  const found = new Set()
  const walk = (n) => {
    if (ts.isIdentifier(n)) found.add(n.text)
    ts.forEachChild(n, walk)
  }
  walk(node)
  return found
}

/** Names bound by a top-level declaration, and how it was initialised. */
function topLevelDeclarations(file) {
  const decls = []
  for (const stmt of file.statements) {
    if (ts.isFunctionDeclaration(stmt) && stmt.name) {
      decls.push({ kind: 'function', name: stmt.name.text, node: stmt })
    } else if (ts.isVariableStatement(stmt)) {
      for (const d of stmt.declarationList.declarations) {
        if (!ts.isIdentifier(d.name)) continue
        const init = d.initializer
        const rune =
          init && ts.isCallExpression(init) && ts.isIdentifier(init.expression) &&
          RUNES.has(init.expression.text)
            ? init.expression.text
            : null
        const isFn = init && (ts.isArrowFunction(init) || ts.isFunctionExpression(init))
        decls.push({
          kind: rune ? 'state' : isFn ? 'function' : 'value',
          name: d.name.text,
          node: init ?? d,
          rune,
        })
      }
    } else if (ts.isImportDeclaration(stmt)) {
      const from = stmt.moduleSpecifier.text
      const names = []
      const clause = stmt.importClause
      if (clause?.name) names.push(clause.name.text)
      if (clause?.namedBindings) {
        if (ts.isNamedImports(clause.namedBindings)) {
          for (const el of clause.namedBindings.elements) names.push(el.name.text)
        } else names.push(clause.namedBindings.name.text)
      }
      for (const name of names) decls.push({ kind: 'import', name, from })
    }
  }
  return decls
}

export function analyse(code) {
  const file = ts.createSourceFile('App.script.ts', code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const decls = topLevelDeclarations(file)

  // Anything that ties a function to THIS component instance.
  const stateNames = new Set(decls.filter((d) => d.kind === 'state').map((d) => d.name))
  const wailsNames = new Set(
    decls.filter((d) => d.kind === 'import' && /wailsjs/.test(d.from)).map((d) => d.name),
  )
  // Svelte's own component API is equally component-bound.
  const svelteNames = new Set(['$effect', '$inspect', '$host', 'onMount', 'onDestroy', 'tick'])

  const functions = decls.filter((d) => d.kind === 'function')
  const byName = new Map(functions.map((f) => [f.name, f]))
  const values = new Set(decls.filter((d) => d.kind === 'value').map((d) => d.name))

  for (const fn of functions) {
    fn.refs = identifiersIn(fn.node)
    fn.touchesState = [...fn.refs].filter((r) => stateNames.has(r))
    fn.touchesWails = [...fn.refs].filter((r) => wailsNames.has(r))
    fn.touchesSvelte = [...fn.refs].filter((r) => svelteNames.has(r) && r !== fn.name)
    // A top-level `const x = {...}` that is not state is still module-level
    // mutable data shared with the markup; treat a reference to one as binding.
    fn.touchesModuleValue = [...fn.refs].filter((r) => values.has(r))
  }

  // Purity is transitive: calling an impure helper makes you impure. Iterate to
  // a fixpoint rather than assuming the call graph is shallow.
  const impure = new Set(
    functions
      .filter(
        (f) =>
          f.touchesState.length || f.touchesWails.length ||
          f.touchesSvelte.length || f.touchesModuleValue.length,
      )
      .map((f) => f.name),
  )
  for (let changed = true; changed; ) {
    changed = false
    for (const fn of functions) {
      if (impure.has(fn.name)) continue
      for (const ref of fn.refs) {
        if (ref !== fn.name && impure.has(ref) && byName.has(ref)) {
          impure.add(fn.name)
          fn.impureVia = ref
          changed = true
          break
        }
      }
    }
  }

  for (const fn of functions) {
    fn.pure = !impure.has(fn.name)
    fn.lines = code.slice(fn.node.pos, fn.node.end).split('\n').length
  }
  return { file, decls, functions, stateNames, wailsNames }
}

const text = readFileSync(SOURCE, 'utf8')
const { code, offset } = scriptBody(text)
const { functions, stateNames, wailsNames } = analyse(code)

const args = process.argv.slice(2)
const pure = functions.filter((f) => f.pure).sort((a, b) => b.lines - a.lines)

if (args[0] === '--why') {
  const fn = functions.find((f) => f.name === args[1])
  if (!fn) {
    console.error(`no top-level function named ${args[1]}`)
    process.exit(1)
  }
  console.log(`${fn.name} — ${fn.lines} lines, ${fn.pure ? 'PURE' : 'component-bound'}`)
  for (const [label, list] of [
    ['component state', fn.touchesState],
    ['Wails bindings', fn.touchesWails],
    ['Svelte API', fn.touchesSvelte],
    ['module values', fn.touchesModuleValue],
  ]) {
    if (list?.length) console.log(`  ${label}: ${list.join(', ')}`)
  }
  if (fn.impureVia) console.log(`  reaches component state through: ${fn.impureVia}`)
  process.exit(0)
}

if (args[0] === '--list') {
  for (const fn of pure) console.log(`${String(fn.lines).padStart(5)}  ${fn.name}`)
  process.exit(0)
}

const pureLines = pure.reduce((n, f) => n + f.lines, 0)
console.log(`${SOURCE}: script starts at line ${offset}`)
console.log(`  top-level functions      ${functions.length}`)
console.log(`  component state          ${stateNames.size}`)
console.log(`  Wails bindings imported  ${wailsNames.size}`)
console.log(`  PURE functions           ${pure.length}  (${pureLines} lines)`)
console.log(`  component-bound          ${functions.length - pure.length}`)
