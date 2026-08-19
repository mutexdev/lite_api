// Gathering every script log in the workspace into one console.
//
// A pm.console.log() lands on the response of the request that produced it, so
// the logs are scattered across every collection and every item. The DevTools
// console is the only place they are visible together, which is what makes
// "which request printed that?" answerable.

import type { types } from '../../wailsjs/go/models'

export type ScriptLog = {
  level: string
  message: string
  args?: string[]
}

export type DevToolsConsoleLog = ScriptLog & {
  collectionName: string
  requestName: string
  /** Breadcrumb of collection / folder / request, for the console's source column. */
  source: string
}

/**
 * The script logs attached to one response.
 *
 * scriptLogs is read through a cast because it is not on the generated Response
 * type: the Go side attaches it, and regenerating the bindings has never
 * surfaced it. Kept as a cast rather than widening the shared type, so the
 * looseness stays in one place.
 */
export function responseScriptLogs(response: types.Response | undefined): ScriptLog[] {
  return (response as unknown as { scriptLogs?: ScriptLog[] })?.scriptLogs ?? []
}

/**
 * Every script log in the workspace, tagged with where it came from.
 *
 * The source breadcrumb omits an empty folder path rather than rendering
 * "Collection /  / Request" — a log whose origin reads with a gap in it looks
 * like the origin was lost.
 *
 * Order is collection, then item, then the order the script emitted them. That
 * is not chronological across requests, and deliberately so: logs from one run
 * belong together, and interleaving two requests by timestamp makes a single
 * script's output impossible to follow.
 */
export function devToolsConsoleLogs(workspace: types.Workspace | undefined): DevToolsConsoleLog[] {
  const rows: DevToolsConsoleLog[] = []
  for (const collection of workspace?.collections ?? []) {
    for (const item of collection.items ?? []) {
      for (const log of responseScriptLogs(item.response)) {
        rows.push({
          ...log,
          collectionName: collection.name,
          requestName: item.name,
          source: [collection.name, item.folderPath, item.name].filter(Boolean).join(' / ')
        })
      }
    }
  }
  return rows
}
