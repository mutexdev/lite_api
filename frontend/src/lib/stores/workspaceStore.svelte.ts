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

  // --- selection -------------------------------------------------------
  //
  // These two are the user's current place in the tree. They are UI state
  // rather than backend state, which is why they live here beside the
  // AppState rather than inside it — the backend has no opinion about which
  // collection a given window is looking at.

  selectedCollectionId = $state('')
  selectedEnvironmentId = $state('')

  get activeTab() {
    return this.appState?.openTabs?.find((tab) => tab.id === this.appState?.activeTabId)
  }

  /**
   * The collection the user explicitly selected, if it still exists.
   *
   * Separate from activeCollection because the fallback chain matters: an
   * explicit selection wins, then the active tab's collection, then the first
   * one. Collapsing them would make a deleted selection silently jump the user
   * to an unrelated collection instead of following their tab.
   */
  get selectedCollection() {
    return this.activeWorkspace?.collections?.find((collection) => collection.id === this.selectedCollectionId)
  }

  get activeCollection() {
    return (
      this.selectedCollection ??
      this.activeWorkspace?.collections?.find((collection) => collection.id === this.activeTab?.collectionId) ??
      this.activeWorkspace?.collections?.[0]
    )
  }

  get activeRequest() {
    return this.activeCollection?.items?.find((item) => item.id === this.activeTab?.itemId) ?? this.activeCollection?.items?.[0]
  }

  get selectedEnvironment() {
    return (
      this.activeCollection?.environments?.find((env) => env.id === this.selectedEnvironmentId) ??
      this.activeCollection?.environments?.[0]
    )
  }

  get activeGlobalEnvironment() {
    return this.activeWorkspace?.globalEnvironments?.find(
      (env) => env.id === this.activeWorkspace?.activeGlobalEnvironmentId
    )
  }

  // --- command bar projection -----------------------------------------
  //
  // The values WorkspaceCommandBar used to receive as separate props. Exposed
  // here so the component reads them directly instead of App.svelte computing
  // each one inline at the call site and passing it down.

  get workspaceOptions() {
    return this.workspaces.map((workspace) => ({ id: workspace.id, name: workspace.name }))
  }

  get globalEnvironmentOptions() {
    return (this.activeWorkspace?.globalEnvironments ?? []).map((environment) => ({
      id: environment.id,
      name: environment.name
    }))
  }

  get environmentOptions() {
    return (this.activeCollection?.environments ?? []).map((environment) => ({
      id: environment.id,
      name: environment.name
    }))
  }

  get environmentName() {
    // Only a chosen environment gets a name. Falling back to the first one's
    // name while none is selected would tell the user they are sending against
    // an environment they never picked.
    return this.selectedEnvironmentId ? (this.selectedEnvironment?.name ?? 'No environment') : 'No environment'
  }

  get canCreateRequest() {
    return Boolean(this.activeCollection)
  }

  get canCreateFolder() {
    return Boolean(this.activeCollection && !this.activeCollection.notFoundLocally)
  }
}

export const workspaceStore = new WorkspaceStore()
export type { WorkspaceStore }
