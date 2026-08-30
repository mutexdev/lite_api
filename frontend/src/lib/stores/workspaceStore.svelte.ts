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
import type { types } from '../../../wailsjs/go/models'
import {
  readEnvironmentSelections,
  resolveEnvironmentId,
  withEnvironmentSelection,
  writeEnvironmentSelections,
  type EnvironmentSelectionMap
} from '../environmentSelection'

class WorkspaceStore {
  /**
   * The authoritative application state, as the Go bindings return it.
   *
   * Null before the first load. Every binding call that returns an AppState
   * assigns it here, which is why it is a plain writable field rather than
   * something with a setter: the call sites are assignments and keeping them
   * that way is what makes the rewrite mechanical and checkable.
   */
  appState = $state<types.AppState | null>(null)

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

  /**
   * Chosen environment per collection, and the window scope it persists under.
   *
   * This replaces a single `selectedEnvironmentId = $state('')`. That field was
   * written once at startup against whichever collection was active THEN, and
   * nothing recomputed it when the user switched collections — so the id went
   * stale while three different fallbacks kept displaying plausible-looking
   * environment names on top of it. Keying by collection is what makes the
   * switch a non-event: every collection carries its own answer.
   */
  environmentSelections = $state<EnvironmentSelectionMap>({})
  private environmentScope = ''

  /**
   * Points the store at this window's persisted selections.
   *
   * Called once the async GetWebStorageScope() resolves. Until then the scope
   * is "", persistence is a no-op, and selections behave as unsaved defaults —
   * see environmentSelectionKey for why an unscoped fallback is not an option.
   */
  bindEnvironmentScope(scope: string) {
    this.environmentScope = scope
    this.environmentSelections = readEnvironmentSelections(scope)
  }

  /**
   * The active environment id for the active collection.
   *
   * A getter, not a field: it is derived from the persisted choice and the
   * collection's current environment list, so it cannot drift out of step with
   * either. Assignment still works — see the setter — which is what lets the
   * existing `workspaceStore.selectedEnvironmentId = x` call sites stand.
   */
  get selectedEnvironmentId(): string {
    const collection = this.activeCollection
    if (!collection) return ''
    return resolveEnvironmentId(collection.environments, this.environmentSelections[collection.id])
  }

  set selectedEnvironmentId(environmentId: string) {
    const collection = this.activeCollection
    if (!collection) return
    this.environmentSelections = withEnvironmentSelection(
      this.environmentSelections,
      collection.id,
      environmentId
    )
    writeEnvironmentSelections(this.environmentScope, this.environmentSelections)
  }

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

  /**
   * The active environment object.
   *
   * The `?? environments[0]` that used to close this expression is deliberately
   * gone. It was the visible half of the split-brain: when the id was stale or
   * empty, this returned the first environment and the command strip displayed
   * its name, while the <select> showed nothing and the backend was handed the
   * empty id. The fallback now lives in resolveEnvironmentId, ONE level down,
   * where it changes the id itself — so the name shown and the id sent are
   * always the same environment.
   */
  get selectedEnvironment() {
    const environmentId = this.selectedEnvironmentId
    if (!environmentId) return undefined
    return this.activeCollection?.environments?.find((env) => env.id === environmentId)
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
