// What can be done to a thing in the sidebar.
//
// THIS IS THE PIECE THAT WAS MISSING. LiteAPI has 38 keyboard commands and a
// fuzzy palette, and yet renaming a request costs three clicks — because
// commandPaletteCommandIDs holds fifteen commands that are all GLOBAL, and the
// eight per-object actions existed only as buttons under a pointer. There was
// nowhere in the app that meant "the thing I have selected", so there was
// nothing for a shortcut to act on.
//
// The registry is deliberately not a tree feature. It answers a question about
// an OBJECT — a collection, a folder, a request — and knows nothing about rows,
// indices or the DOM. That is what lets the same list drive both the pointer
// menu and the keyboard menu, and what will let a future caller ask the same
// question about the request in the active tab without any of this changing.
//
// ONE CANONICAL ORDER SERVES EVERY ROW TYPE. The two shipped button rows —
// a folder's seven single letters (F i T + ✎ C x) and a request's six words
// behind a disclosure — were two designs for one question, and both are now
// gone: every row opens the same menu, rendering the labels below.
//
// The registry first reproduced those two orders exactly, so that unifying them
// changed nothing on screen. That constraint was right while the change was
// meant to be invisible, and stopped applying once the row toolbar itself became
// the thing being fixed.

export type SidebarObjectKind = 'collection' | 'folder' | 'request' | 'flow'

/**
 * A thing the user can act on.
 *
 * Flat rather than a discriminated union because callers pass it straight
 * through from a SidebarRow, and narrowing at every hop buys nothing when the
 * unused fields are always ''.
 */
export type SidebarObject = {
  kind: SidebarObjectKind
  collectionId: string
  /** '' for a collection, a flow, or a request outside any folder. */
  folder: string
  /** The request's id on a request, the flow's id on a flow, '' otherwise. */
  itemId: string
  label: string
}

export type SidebarActionID =
  | 'new-request'
  | 'new-flow'
  | 'run-collection'
  | 'reveal'
  | 'generate-code'
  | 'info'
  | 'open-terminal'
  | 'new-folder'
  | 'rename'
  | 'clone'
  | 'delete'

export type SidebarAction = {
  id: SidebarActionID
  /** Spoken and shown in the keyboard menu. */
  label: string
  /** Rendered beside the label in the keyboard menu, and bound directly. */
  shortcut?: string
  tone?: 'danger'
  testId: string
}

export type SidebarActionContext = {
  /** Platform-specific: "Reveal in Finder", "Reveal in File Explorer", … */
  revealLabel: string
  /** Only http and graphql requests can generate code. */
  supportsGenerateCode: boolean
  /**
   * The formatted binding for a keybinding action, or '' when unbound.
   *
   * Passed in rather than looked up here so the menu shows the user's OWN
   * shortcut. Bindings are customisable and preset-switchable; a hardcoded
   * "⌘R" in this file would be wrong for anybody who changed it, and wrong
   * silently.
   */
  shortcutFor?: (bindingAction: string) => string
}

/**
 * The keybinding action behind each directly-bound sidebar action.
 *
 * THESE BINDINGS ALREADY EXISTED AND ALREADY DID NOTHING. keybindings.ts ships
 * a "Sidebar" section that Preferences renders in full, advertising ⌘R to
 * rename an item and ⌘D to clone one. Neither appears in shortcuts.ts, so
 * neither has ever fired — the app has been promising this feature in its own
 * settings screen without implementing it. Wiring the promised keys is
 * therefore the correct move; inventing a second vocabulary (F2, say) would
 * have left the Preferences rows just as dead as they were.
 *
 * `deleteItem` is new, and follows the platform convention its neighbours do:
 * Command-Delete is "move to trash" on macOS.
 *
 * copyItem and pasteItem are the other two dead entries. They are NOT wired
 * here: a clipboard for requests is a data-model question — what a copied
 * request is, where a paste lands, what happens across collections — and not
 * something to answer as a side effect of adding keyboard navigation.
 */
export const SIDEBAR_ACTION_BINDINGS: Partial<Record<SidebarActionID, string>> = {
  rename: 'renameItem',
  clone: 'cloneItem',
  delete: 'deleteItem'
}

/**
 * Canonical order: create, then inspect, then modify, then destroy.
 *
 * The creating actions lead because they are the ones people reach a folder's
 * menu FOR — a folder exists to be filled. Delete is last and alone, so the
 * destructive entry is never adjacent to the one above it in muscle memory.
 *
 * This order supersedes the two shipped button rows, which the registry
 * originally reproduced exactly. That constraint was right while the change was
 * meant to be invisible; it stopped applying when the row toolbar itself became
 * the thing being fixed.
 */
const ORDER: readonly SidebarActionID[] = [
  'new-request',
  'new-folder',
  // Last of the creating actions, because it is the rarest: a collection gains
  // requests constantly and a flow occasionally. It is on the COLLECTION only —
  // flows are stored in the collection's root config file and have no folder
  // path, so offering "New Flow" on a folder would promise a placement that
  // does not exist.
  'new-flow',
  // A COLLECTION'S PRIMARY VERB, and the only entry here that runs something
  // rather than creating or inspecting it. The top bar carried a Run button
  // beside the environment picker, which put "execute every request in the
  // collection" one click from every screen in the app whether or not a
  // collection was even the thing being looked at. It belongs on the object it
  // acts on. Placed at the boundary between the creating actions and the
  // inspecting ones because it is neither, and because Reveal must not be the
  // row that sits under a mis-aimed click meant for New Flow.
  'run-collection',
  'reveal',
  'generate-code',
  'info',
  'open-terminal',
  'rename',
  'clone',
  'delete'
]

const TEST_IDS: Record<SidebarActionID, string> = {
  'new-request': 'collection-item-menu-new-request',
  'new-flow': 'collection-item-menu-new-flow',
  'run-collection': 'collection-item-menu-run-collection',
  reveal: 'collection-item-menu-show-in-folder',
  'generate-code': 'collection-item-menu-generate-code',
  info: 'collection-item-menu-info',
  'open-terminal': 'collection-item-menu-open-terminal',
  'new-folder': 'collection-item-menu-new-folder',
  rename: 'collection-item-menu-rename',
  clone: 'collection-item-menu-clone',
  delete: 'collection-item-menu-delete'
}

/** Which actions each kind of object offers, in no particular order. */
const AVAILABLE: Record<SidebarObjectKind, ReadonlySet<SidebarActionID>> = {
  // A collection offers the creating actions and Run. Rename, clone and delete
  // exist for collections but live in the collection settings pane and take a
  // different shape (they move directories on disk), so putting them on the row
  // is a separate decision rather than a free addition here.
  collection: new Set<SidebarActionID>(['new-request', 'new-folder', 'new-flow', 'run-collection']),
  folder: new Set<SidebarActionID>([
    'new-request', 'new-folder', 'reveal', 'info', 'open-terminal', 'rename', 'clone', 'delete'
  ]),
  request: new Set<SidebarActionID>(['reveal', 'generate-code', 'info', 'rename', 'clone', 'delete']),
  // A FLOW OFFERS TWO ACTIONS, AND THE SHORT LIST IS THE POINT.
  //
  // Until now it offered none: the flow row was a bare button with no ⋯, no
  // right-click and no keyboard route, so a flow could not be deleted from the
  // sidebar at all — only from inside its own open tab. Reveal and Delete are
  // the two that already have working handlers behind them, so both entries
  // do something the moment they appear.
  //
  // RENAME IS ABSENT DELIBERATELY, and this is the same judgement the file
  // already makes about copyItem and pasteItem above: there is no
  // RenameFlowModal, and renaming a flow is an UpdateFlow round trip that
  // needs somewhere to type the new name. Listing an action whose handler does
  // not exist is how a menu entry becomes decoration — the exact failure the
  // dead ⌘R and ⌘D bindings in Preferences already demonstrated. Add 'rename'
  // to this set in the same change that adds the dialog, not before.
  //
  // Reveal opens the collection's own directory rather than a file of the
  // flow's own, because a flow has no file of its own: it is stored in the
  // collection's root config. That is the honest destination, and it is the
  // one the collection's own reveal would give.
  flow: new Set<SidebarActionID>(['reveal', 'delete'])
}

function labelFor(id: SidebarActionID, context: SidebarActionContext): string {
  if (id === 'reveal') return context.revealLabel
  return {
    'new-request': 'New Request',
    'new-flow': 'New Flow',
    'run-collection': 'Run collection',
    'generate-code': 'Generate Code',
    info: 'Info',
    'open-terminal': 'Open in Terminal',
    'new-folder': 'New Folder',
    rename: 'Rename',
    clone: 'Clone',
    delete: 'Delete'
  }[id] ?? id
}

/**
 * The actions available on one object, in the order they are drawn.
 *
 * `supportsGenerateCode` is the only capability that varies within a kind: a
 * gRPC or WebSocket request offers no code generation, and the shipped markup
 * omitted the button entirely rather than disabling it. That behaviour is
 * preserved — an action that cannot run is absent, not greyed out, because a
 * disabled entry in a keyboard menu is a row the user still has to arrow past.
 */
export function sidebarActionsFor(
  object: SidebarObject,
  context: SidebarActionContext
): SidebarAction[] {
  const available = AVAILABLE[object.kind]

  return ORDER.filter((id) => available.has(id))
    .filter((id) => id !== 'generate-code' || context.supportsGenerateCode)
    .map((id) => {
      const binding = SIDEBAR_ACTION_BINDINGS[id]
      // An empty display value means the user cleared the binding, and an
      // empty hint is dropped rather than rendered as a blank column.
      const shortcut = binding ? context.shortcutFor?.(binding) || undefined : undefined
      return {
        id,
        label: labelFor(id, context),
        shortcut,
        testId: TEST_IDS[id],
        ...(id === 'delete' ? { tone: 'danger' as const } : {})
      }
    })
}

/** The object a sidebar row acts on, or undefined for rows that have none. */
export function sidebarObjectForRow(row: {
  kind: string
  collectionId: string
  folder: string
  itemId: string
  label: string
}): SidebarObject | undefined {
  if (row.kind === 'folder' || row.kind === 'request' || row.kind === 'collection' || row.kind === 'flow') {
    return {
      kind: row.kind,
      collectionId: row.collectionId,
      folder: row.folder,
      itemId: row.itemId,
      label: row.label
    }
  }
  // A response example row has no actions of its own: it is a view of a
  // request, and the request's own actions are one row up.
  return undefined
}
