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
