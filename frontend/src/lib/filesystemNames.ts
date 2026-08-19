// Validation for the names the user types into the new/rename/clone dialogs.
//
// These are not display names. Each one becomes a real directory or file inside
// the collection on disk, so the checks here are the last thing between a typed
// string and a path the backend will open. The backend validates again — but by
// then the dialog has already told the user their name was fine, and the only
// feedback is a failed action with a message about a path they never saw.
//
// Three separate hazards, deliberately kept apart because they have different
// answers:
//
//   1. Characters a filesystem cannot store, or that change what a path means.
//   2. Names Windows reserves for devices, which fail on one platform only.
//   3. Names this app reserves for its own metadata files, which would be read
//      back as something other than what was written.

/** Characters that are illegal in a path segment, or that would split one. */
const INVALID_CHARACTERS = /[<>:"/\\|?*\x00-\x1F]/g

/**
 * Windows device names, which cannot be used as a filename on that platform at
 * any extension. A collection created on macOS with a folder named "aux" is
 * simply unopenable once it is synced to Windows.
 */
const RESERVED_DEVICE_NAMES = /^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$/i

/** A leading space or dash — the first hides the name, the second reads as a flag. */
const FIRST_CHARACTER = /^[^\s\-<>:"/\\|?*\x00-\x1F]/
const MIDDLE_CHARACTERS = /^[^<>:"/\\|?*\x00-\x1F]*$/
/**
 * A trailing dot or space.
 *
 * This is also what rejects "." and "..": both end in a dot, so traversal
 * segments never reach the backend as a directory name. That is a consequence
 * of the rule rather than its stated purpose, which is why it is called out
 * here — anyone relaxing the trailing-dot rule would reopen it.
 */
const LAST_CHARACTER = /[^.\s<>:"/\\|?*\x00-\x1F]$/

const MAX_NAME_LENGTH = 255

/**
 * The basenames this app writes its own metadata under.
 *
 * A request saved as "folder" produces folder.bru, which the reader treats as
 * the enclosing folder's settings rather than as a request — the request
 * disappears and the folder's configuration is replaced by it.
 */
const RESERVED_METADATA_NAMES = ['collection', 'folder']

/**
 * Replaces illegal characters and trims the edges that would make a name
 * invalid.
 *
 * This is the suggestion offered as the user types a display name, not a
 * guarantee: it can still produce "" (from a name that was entirely illegal
 * characters) or a reserved name, both of which `collectionFolderNameIsValid`
 * then rejects. Sanitizing and validating are separate steps on purpose.
 */
export function sanitizeCollectionFolderName(name: string): string {
  return name
    .replace(INVALID_CHARACTERS, '-')
    .replace(/^[\s-]+/, '')
    .replace(/[.\s]+$/, '')
}

/** Whether a name is storable as a single path segment on every platform. */
export function collectionFolderNameIsValid(name: string): boolean {
  if (!name || name.length > MAX_NAME_LENGTH) return false
  if (RESERVED_DEVICE_NAMES.test(name)) return false
  return FIRST_CHARACTER.test(name) && MIDDLE_CHARACTERS.test(name) && LAST_CHARACTER.test(name)
}

/**
 * Whether a name collides with one of this app's metadata files.
 *
 * Compared case-insensitively after trimming, because the filesystems this runs
 * on are mostly case-insensitive: "Folder" and "folder" are the same file on
 * macOS and Windows, and only differ on Linux.
 */
export function isReservedMetadataName(name: string): boolean {
  return RESERVED_METADATA_NAMES.includes(name.trim().toLowerCase())
}

/**
 * The full check every new/rename/clone dialog applies to a filesystem name.
 *
 * The two halves are both required and neither implies the other — "folder" is
 * a perfectly legal path segment, and "aux.bru" is not reserved metadata.
 */
export function filesystemNameIsValid(name: string): boolean {
  return collectionFolderNameIsValid(name) && !isReservedMetadataName(name)
}

/**
 * Whether a new top-level directory would collide with the collection's own
 * environments directory.
 *
 * Only applies at the collection root: nested folders have no environments
 * directory beside them, and rejecting the name there would be a restriction
 * with nothing behind it.
 *
 * The match is a SUBSTRING, not an equality — so "environments backup" is
 * rejected too. That is broader than the collision it guards, and is preserved
 * here exactly as it behaved before this module existed; narrowing it is a
 * behaviour change and does not belong in a move.
 */
export function isReservedRootDirectory(parentPath: string, name: string): boolean {
  return parentPath === '' && name.trim().toLowerCase().includes('environments')
}

/** The last segment of a path, accepting either separator. */
export function slashPathBase(value: string | undefined): string {
  const parts = (value ?? '').replaceAll('\\', '/').split('/').filter(Boolean)
  return parts.at(-1) ?? ''
}

/**
 * The three below came out of App.svelte, where they sat 4,000 lines from
 * `slashPathBase` and did the same algebra on the same paths.
 *
 * NOTE, and left deliberately unchanged: `slashPathBase` above accepts either
 * separator, and these three accept only `/`. That asymmetry is not obvious
 * until they are read side by side, which is most of the argument for moving
 * them here. It is safe today because these operate on collection-tree paths,
 * which are built by `joinSlashPath` and are therefore always `/`-joined — a
 * Windows filesystem path would give the wrong answer. Changing it is a
 * behaviour change and does not belong in a move.
 */

/** A tree path with its last segment removed. `''` when there is no parent. */
export function slashPathParent(value: string | undefined): string {
  const parts = (value ?? '').split('/').filter(Boolean)
  parts.pop()
  return parts.join('/')
}

/**
 * Whether `value` is `prefix` or sits beneath it.
 *
 * The boundary matters: `a/bc` is NOT beneath `a/b`, so the check appends a
 * separator rather than using a bare `startsWith`. Getting that wrong would
 * make a sibling folder register as a descendant, which is how a drag-and-drop
 * ends up refusing a legal move or permitting a folder into its own subtree.
 */
export function slashPathHasPrefix(
  value: string | undefined,
  prefix: string | undefined,
): boolean {
  const normalizedValue = (value ?? '').split('/').filter(Boolean).join('/')
  const normalizedPrefix = (prefix ?? '').split('/').filter(Boolean).join('/')
  return (
    normalizedValue !== '' &&
    normalizedPrefix !== '' &&
    (normalizedValue === normalizedPrefix ||
      normalizedValue.startsWith(`${normalizedPrefix}/`))
  )
}

/** Joins a parent and child, dropping either if empty so no `//` appears. */
export function joinSlashPath(parent: string, child: string): string {
  return [parent, child].filter(Boolean).join('/')
}

/**
 * A request's filesystem basename, with its format extension removed.
 *
 * Falls back through the display name to a constant, because a request created
 * in memory and never saved has no file path yet, and an empty rename dialog is
 * indistinguishable from a broken one.
 */
export function requestFilesystemBaseName(
  filePath: string | undefined,
  displayName: string | undefined,
): string {
  const fileName = slashPathBase(filePath ?? '')
  const withoutExtension = fileName.replace(/\.(bru|ya?ml)$/i, '')
  return sanitizeCollectionFolderName(withoutExtension || displayName || 'Request')
}

/**
 * The display name for a certificate path chosen through a file dialog.
 *
 * Falls back to the whole path when the basename comes out empty (a path that
 * is nothing but separators), because an empty label beside a "Clear" button
 * reads as no certificate being configured at all.
 */
export function certificateFileName(filePath: string | undefined): string {
  const trimmed = filePath?.trim() ?? ''
  if (!trimmed) return ''
  return slashPathBase(trimmed) || trimmed
}
