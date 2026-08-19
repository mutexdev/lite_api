export type GitWorkbenchFile = {
  path: string
  staged: boolean
  untracked: boolean
  conflicted: boolean
}

/** Keep selections valid after a status refresh without selecting new files. */
export function reconcileGitSelection(selected: readonly string[], files: readonly GitWorkbenchFile[]): string[] {
  const available = new Set(files.map((file) => file.path))
  return [...new Set(selected)].filter((path) => available.has(path))
}

export function canSwitchGitBranch(snapshot: { clean: boolean; conflicts: boolean } | undefined): boolean {
  return Boolean(snapshot?.clean && !snapshot.conflicts)
}

export function canStageGitSelection(selected: readonly string[], files: readonly GitWorkbenchFile[]): boolean {
  const selectedSet = new Set(selected)
  // A conflicted row reports as staged in porcelain. After the user resolves
  // the file manually, explicitly staging that same row is how Git records
  // the resolution, so it must remain eligible here.
  return files.some((file) => selectedSet.has(file.path) && (file.conflicted || !file.staged))
}

export function canUnstageGitSelection(selected: readonly string[], files: readonly GitWorkbenchFile[]): boolean {
  const selectedSet = new Set(selected)
  return files.some((file) => selectedSet.has(file.path) && file.staged && !file.conflicted)
}

export function canPushGitBranch(upstream: string | undefined, remote: string, branch: string, setUpstream: boolean): boolean {
  return setUpstream || upstream === `${remote}/${branch}`
}

/** Follow the active Git branch until the user deliberately chooses another remote branch. */
export function reconcileGitRemoteBranch(current: string, previousBranch: string | undefined, nextBranch: string | undefined): string {
  const next = nextBranch ?? ''
  if (!current || current === (previousBranch ?? '')) return next
  return current
}

export type GitWorkbenchRemote = { name: string; url: string }

export type GitRemoteSelection = { name: string; url: string }

/**
 * Picks which remote the workbench shows after a refresh.
 *
 * Keeps the one already selected when it still exists, so a refresh does not
 * move the user off the remote they were about to push to. Falls back to the
 * first remote, because a repository with remotes and none selected offers a
 * push button that cannot work.
 *
 * With no remotes at all the URL is CLEARED rather than left alone. A stale URL
 * beside an empty remote list reads as a configured remote, and the connect
 * dialog would open pre-filled with an address the repository no longer has.
 */
export function reconcileGitRemoteSelection(
  currentName: string,
  remotes: readonly GitWorkbenchRemote[] | undefined
): GitRemoteSelection {
  const list = remotes ?? []
  const selected = list.find((remote) => remote.name === currentName) ?? list[0]
  if (!selected) return { name: currentName, url: '' }
  return { name: selected.name, url: selected.url }
}

/**
 * The branch the workbench shows after a refresh.
 *
 * Keeps the current choice only while it is still a branch that exists —
 * otherwise the checkout button would target a branch that was deleted or
 * renamed elsewhere, and git would refuse with a message about a ref rather
 * than about the branch picker.
 */
export function reconcileGitBranch(
  current: string,
  branches: readonly string[] | undefined,
  head: string | undefined
): string {
  if (current && (branches ?? []).includes(current)) return current
  return head ?? ''
}
