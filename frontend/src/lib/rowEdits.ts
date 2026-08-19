// Reordering and rewriting rows in a key/value table.
//
// Every one of these tables — params, headers, form fields, multipart parts,
// file rows, and the same six again inside a response example — drives its
// buttons and its drag handle through these four functions. That is why they
// are generic and why they live here: the bounds arithmetic is the same
// arithmetic in a dozen places, and an off-by-one in it does not throw. It
// drops a row, or duplicates one, or silently ignores a drag.
//
// Every function returns a NEW array, including on the paths where nothing
// changes. Returning the input unchanged would work for the value but not for
// the UI: Svelte's reactivity is identity-based, so a handler that returns the
// same array leaves the table showing its pre-drag order until something else
// happens to invalidate it.

import type { types } from '../../wailsjs/go/models'


/**
 * Moves one row up or down by a single position.
 *
 * A move off either end returns the list unchanged instead of wrapping: the
 * up-arrow on the first row is visible and clickable, and wrapping it to the
 * bottom would be a reorder the user did not ask for.
 */
export function movedRows<T>(rows: readonly T[] | undefined, index: number, direction: -1 | 1): T[] {
  const next = [...(rows ?? [])]
  const target = index + direction
  if (index < 0 || target < 0 || index >= next.length || target >= next.length) return next
  const [row] = next.splice(index, 1)
  next.splice(target, 0, row)
  return next
}

/**
 * Moves one row to an arbitrary position, for a drag.
 *
 * The `Math.min` after the removal is the part that is easy to lose: the array
 * is one shorter once the dragged row is spliced out, so dropping onto the last
 * position gives an insertion index equal to the new length. Without the clamp
 * `splice` still appends, but only by accident — and any later change to the
 * bounds check above would turn that accident into a dropped row.
 */
export function reorderedRows<T>(rows: readonly T[] | undefined, from: number, to: number): T[] {
  const next = [...(rows ?? [])]
  if (from < 0 || to < 0 || from >= next.length || to >= next.length || from === to) return next
  const [row] = next.splice(from, 1)
  next.splice(Math.min(to, next.length), 0, row)
  return next
}

/**
 * Fills in the fields the bulk editor does not parse.
 *
 * The bulk editor reads "name: value" lines, so it has nothing to say about
 * `secret` or `description`. Leaving them undefined is not the same as leaving
 * them alone — the rows REPLACE the table wholesale, and an undefined `secret`
 * serialises as a missing field, which reads back as not-secret anyway but
 * writes a different file each time the same table is saved.
 */
export function normalizeBulkKeyValueRows(
  rows: readonly { name: string; value: string; enabled: boolean; secret?: boolean; description?: string }[]
): types.KeyValue[] {
  return rows.map(
    (row) =>
      ({
        name: row.name,
        value: row.value,
        enabled: row.enabled,
        secret: row.secret ?? false,
        description: row.description ?? ''
      }) as types.KeyValue
  )
}
