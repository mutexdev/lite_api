// US-026 — the workspace store.
//
// Owns the single AppState the whole app reads. It lived as a local in
// App.svelte, which meant every component wanting any part of it had to be
// handed that part as a prop — the drilling this story exists to remove.
//
// The migration is safe in a way the earlier Svelte work was not: App.svelte
// keeps reading through a `$derived` alias, so its ~170 read sites are
// untouched, and every one of its ~120 assignment sites MUST be rewritten
// because assigning to a $derived is a compile error. There is no silent
// half-migrated state available here.
import type { main } from '../../../wailsjs/go/models'

class WorkspaceStore {
  /**
   * The authoritative application state, as the Go bindings return it.
   *
   * Null before the first load. Every binding call that returns an AppState
   * assigns it here, which is why it is a plain writable field rather than
   * something with a setter: the call sites are assignments and keeping them
   * that way is what makes the rewrite mechanical and checkable.
   */
  appState = $state<main.AppState | null>(null)

  get workspaces() {
    return this.appState?.workspaces ?? []
  }

  get activeWorkspaceId() {
    return this.appState?.activeWorkspaceId ?? ''
  }

  get activeWorkspace() {
    return this.workspaces.find((workspace) => workspace.id === this.activeWorkspaceId)
  }

  get preferences() {
    return this.appState?.preferences
  }

  /** Bumped by the backend on every mutation; the memo keys in US-034 read it. */
  get revision() {
    return this.appState?.revision ?? 0
  }
}

export const workspaceStore = new WorkspaceStore()
export type { WorkspaceStore }
