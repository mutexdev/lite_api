<script lang="ts">
  // US-036 — the Notifications dialog, lifted out of App.svelte so its markup
  // is not in the initial chunk. Imported dynamically from inside the {#if}
  // that gates it; a static import would leave it in the initial graph.
  import type { types } from '../../../wailsjs/go/models'
  import Modal from './Modal.svelte'
  import IconButton from '../ui/IconButton.svelte'

  export let unreadNotificationCount: number
  // Mirrors App.svelte's local NotificationTab. Declared here rather than
  // widened to `string`: widening made the prop accept a value setNotificationTab
  // cannot take, which svelte-check rejected at the call site — the error was
  // the type system doing its job, not an obstacle to route around.
  type NotificationTab = 'all' | 'unread'
  export let notificationTab: NotificationTab
  export let visibleNotifications: types.Notification[]
  export let listedNotifications: types.Notification[]
  export let selectedNotification: types.Notification | undefined
  export let busy: string
  export let setNotificationTab: (tab: NotificationTab) => void
  export let markAllNotificationsRead: () => void
  export let clearNotifications: () => void
  export let selectNotification: (notification: types.Notification) => void
  export let notificationTitle: (notification: types.Notification) => string
  export let notificationDate: (notification: types.Notification) => string
  export let notificationType: (notification: types.Notification) => string
  export let notificationDescription: (notification: types.Notification) => string
  export let notificationLevelClass: (notification: types.Notification) => string
  export let closeNotifications: () => void
</script>

<!--
  dialogClass now leads with prompt-dialog, which it did not before.

  This was one of three dialogs that took no part in the shared box treatment:
  .notification-modal re-declared the border, radius, background and shadow that
  .prompt-dialog already sets, to the same values, so restyling the shell meant
  finding and editing four rules instead of one. Composing means the duplicates
  can go.

  PAIRED WITH A style.css EDIT, which is in the handoff and has to land with
  this line: .prompt-dialog also sets padding, and this dialog is an
  edge-to-edge list/detail grid that must have none. Until .notification-modal
  declares `padding: 0`, this dialog renders with a 16px inset it does not want.
-->
<Modal labelledBy="notifications-title" onClose={closeNotifications} dialogClass="prompt-dialog notification-modal" size="large" busy={busy !== ''}>
      <header>
        <div>
          <h2 id="notifications-title">Notifications</h2>
          <p>{unreadNotificationCount} unread</p>
        </div>
        <IconButton icon="close" label="Close" onclick={closeNotifications} />
      </header>
      <div class="notification-tabs">
        <div class="segmented compact">
          <button type="button" class:active={notificationTab === 'all'} on:click={() => setNotificationTab('all')}>All</button>
          <button type="button" class:active={notificationTab === 'unread'} on:click={() => setNotificationTab('unread')}>
            Unread
            {#if unreadNotificationCount > 0}
              <span class="notification-tab-count">{unreadNotificationCount}</span>
            {/if}
          </button>
        </div>
        <div class="button-row compact">
          <button type="button" on:click={markAllNotificationsRead} disabled={unreadNotificationCount === 0 || busy !== ''}>Mark All as Read</button>
          <button type="button" on:click={clearNotifications} disabled={visibleNotifications.length === 0 || busy !== ''}>Clear All</button>
        </div>
      </div>
      <div class="notification-content">
        <div class="notification-list" aria-label="Notifications list">
          {#if listedNotifications.length === 0}
            <div class="notification-empty">No notifications to show.</div>
          {:else}
            {#each listedNotifications as notification (notification.id)}
              <button
                type="button"
                class:active={selectedNotification?.id === notification.id}
                class:unread={!notification.read}
                on:click={() => selectNotification(notification)}
              >
                <span class="notification-list-title">{notificationTitle(notification)}</span>
                <span>{notificationDate(notification)}</span>
              </button>
            {/each}
          {/if}
        </div>
        <article class="notification-detail">
          {#if selectedNotification}
            <div class="notification-detail-meta">
              <span class={`notification-type ${notificationLevelClass(selectedNotification)}`}>{notificationType(selectedNotification)}</span>
              <span>{notificationDate(selectedNotification)}</span>
            </div>
            <h3>{notificationTitle(selectedNotification)}</h3>
            <p>{notificationDescription(selectedNotification)}</p>
          {:else}
            <div class="notification-empty">You are all caught up!</div>
          {/if}
        </article>
      </div>
</Modal>
