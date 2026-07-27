// Splitting a URL into the segments the variable overlay renders.

import type { types } from '../../wailsjs/go/models'
import { isValidVariableName, pathParamTooltipInfo, type VariableTooltipInfo } from './variableResolution.ts'

const promptVariableTextPattern = /^\?([^{}\s](?:[^{}]*?[^{}\s])?)$/

export type URLVariableSegment =
  | {
      key: string
      text: string
      variable: false
      prompt: false
    }
  | {
      key: string
      text: string
      variable: false
      prompt: true
      name: string
    }
  | {
      key: string
      text: string
      variable: true
      prompt: false
      path?: boolean
      name: string
      info: VariableTooltipInfo
    }

/**
 * What the overlay shows for a name it has no resolution for.
 *
 * Editable when the name is valid — the tooltip's whole purpose for an unknown
 * variable is to offer to define it. An invalid name is read-only because there
 * is nothing that could be written that would resolve.
 */
export function fallbackVariableTooltipInfo(name: string): VariableTooltipInfo {
  const validName = isValidVariableName(name)
  return {
    name,
    scope: 'Request',
    rawValue: '',
    resolvedValue: '',
    secret: false,
    readOnly: !validName,
    found: false,
    editable: validName,
    validName,
    source: validName ? 'missing' : 'invalid',
    index: -1
  }
}


/**
 * A URL split into plain text and the tokens the overlay renders specially.
 *
 * Two token syntaxes, and the difference matters. `{{name}}` is a variable and
 * resolves from the scope chain; `/:name` is a PATH PARAMETER and resolves from
 * the request's own table. They render alike and come from different places, so
 * a segment that mislabels one shows a tooltip pointing at the wrong editor.
 *
 * Path tokens are only recognised when pathParams is supplied. The URL bar
 * passes it; other callers rendering the same overlay over a header or body
 * value do not, because a ":" there is ordinary text — a timestamp, a port
 * number, a JSON key.
 *
 * `key` is the keyed-each identity and carries the match offset, so two
 * occurrences of the same variable in one URL stay distinct rows. Without the
 * offset Svelte reuses one node for both and a tooltip opens over the wrong
 * occurrence.
 */
export function urlVariableSegments(value: string, infos: VariableTooltipInfo[], pathParams?: types.KeyValue[]): URLVariableSegment[] {
  const segments: URLVariableSegment[] = []
  const infoByName = new Map(infos.map((info) => [info.name, info]))
  const tokenPattern = pathParams
    ? /\{\{([^{}]+?)\}\}|\/:([^/?&=]+)/g
    : /\{\{([^{}]+?)\}\}/g
  let cursor = 0
  let match: RegExpExecArray | null
  while ((match = tokenPattern.exec(value)) !== null) {
    if (match.index > cursor) {
      segments.push({ key: `text:${cursor}`, text: value.slice(cursor, match.index), variable: false, prompt: false })
    }
    if (match[2] !== undefined) {
      const name = match[2]
      segments.push({
        key: `path:${match.index}:${name}`,
        text: match[0],
        variable: true,
        prompt: false,
        path: true,
        name,
        // `?? []` is for the COMPILER, not for runtime. Reaching here means the
        // pattern had a group 2, which only exists when pathParams was
        // supplied — but that correlation is beyond the type system, so the
        // narrowing has to be written out. It is a branch no test can cover.
        info: pathParamTooltipInfo(name, pathParams ?? [])
      })
      cursor = match.index + match[0].length
      continue
    }
    // Group 1 is guaranteed here: group 2 is handled above and `continue`s, and
    // the pattern has no third alternative. It carried a `?? ''` that read as
    // protection against an undefined group — misleading, since what actually
    // guarantees it is the control flow, not a fallback.
    const rawName = match[1]
    const name = rawName.trim()
    if (promptVariableTextPattern.test(rawName)) {
      segments.push({
        key: `prompt:${match.index}:${rawName}`,
        text: match[0],
        variable: false,
        prompt: true,
        name: rawName.slice(1)
      })
      cursor = match.index + match[0].length
      continue
    }
    segments.push({
      key: `var:${match.index}:${name}`,
      text: match[0],
      variable: true,
      prompt: false,
      name,
      info: infoByName.get(name) ?? fallbackVariableTooltipInfo(name)
    })
    cursor = match.index + match[0].length
  }
  if (cursor < value.length) {
    segments.push({ key: `text:${cursor}`, text: value.slice(cursor), variable: false, prompt: false })
  }
  return segments
}
