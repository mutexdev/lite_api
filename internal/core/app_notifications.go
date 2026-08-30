package core

// The notification centre: raising them and marking them read.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"fmt"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// notificationPushEvent is the "notification" payload.
//
// Only error- and warning-level notifications are pushed. Success and info are
// left pull-only: they are the running commentary of the user's own actions,
// every one of them already rides back on the AppState the binding returns, and
// pushing them would double-render every "Saved X". An error raised by
// something the user did NOT trigger — the collection watcher, the background
// persist writer, a decrypt failure during hydration — has no binding return to
// ride on and reached the UI only if the user happened to reopen the
// notification list afterwards.
type notificationPushEvent struct {
	Notification Notification `json:"notification"`
}

// pushNotification emits an error- or warning-level notification to the
// frontend, following the EventsEmit pattern in session_event_push.go.
//
// a.ctx is nil in every test and until Wails calls startup, and
// wailsruntime.EventsEmit dereferences it to find the event manager, so the nil
// check is load-bearing rather than defensive.
func (a *App) pushNotification(notification Notification) {
	if !notificationLevelPushed(notification.Level) {
		return
	}
	// notificationEmit is the test seam, the same one the live sessions use for
	// their event pushes: EventsEmit needs a Wails context that no test has.
	if a.notificationEmit != nil {
		a.notificationEmit(notification)
		return
	}
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "notification", notificationPushEvent{Notification: notification})
}

// notificationLevelPushed reports whether a level is pushed rather than left
// for the frontend to pull with the next AppState.
func notificationLevelPushed(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "danger", "warning", "warn":
		return true
	default:
		return false
	}
}

func (a *App) MarkNotificationRead(notificationID string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for i := range a.state.Notifications {
		if a.state.Notifications[i].ID == notificationID {
			a.state.Notifications[i].Read = true
			return a.state, a.markDirty(persistScopeState)
		}
	}
	return AppState{}, fmt.Errorf("notification %s not found", notificationID)
}

func (a *App) MarkAllNotificationsRead() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	for i := range a.state.Notifications {
		a.state.Notifications[i].Read = true
	}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) ClearNotifications() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return AppState{}, err
	}
	a.state.Notifications = []Notification{}
	return a.state, a.markDirty(persistScopeState)
}

func (a *App) notify(level, message string) {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		level = "info"
	}
	message = strings.TrimSpace(message)
	notification := Notification{
		ID:          newID("notification"),
		Level:       level,
		Type:        notificationType(level),
		Title:       notificationTitle(level, message),
		Message:     message,
		Description: message,
		Color:       notificationColor(level),
		At:          time.Now(),
	}
	a.state.Notifications = append([]Notification{notification}, a.state.Notifications...)
	if len(a.state.Notifications) > 20 {
		a.state.Notifications = a.state.Notifications[:20]
	}
	a.pushNotification(notification)
}

// notifyChangedLocked raises a notification only when this channel's message
// has changed since the last call for it.
//
// For conditions reported by a poller. The collection watcher runs on a timer,
// so a collection that cannot be read raises the same message on every tick;
// without this the 20-entry notification list would be nothing but copies of it
// within a minute. An empty message clears the channel, so a condition that
// goes away and comes back is reported again rather than suppressed forever.
//
// a.mu must be held.
func (a *App) notifyChangedLocked(channel, level, message string) {
	message = strings.TrimSpace(message)
	if a.notifiedChannels == nil {
		a.notifiedChannels = map[string]string{}
	}
	if a.notifiedChannels[channel] == message {
		return
	}
	a.notifiedChannels[channel] = message
	if message == "" {
		return
	}
	a.notify(level, message)
}

func notificationTitle(level, message string) string {
	message = strings.TrimSpace(message)
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success":
		if message != "" {
			return message
		}
		return "Success"
	case "warning", "warn":
		if message != "" {
			return message
		}
		return "Warning"
	case "error", "danger":
		if message != "" {
			return message
		}
		return "Error"
	default:
		if message != "" {
			return message
		}
		return "Notification"
	}
}

func notificationType(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success":
		return "Success"
	case "warning", "warn":
		return "Warning"
	case "error", "danger":
		return "Error"
	default:
		return "Info"
	}
}

func notificationColor(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "success":
		return "#1f7a5a"
	case "warning", "warn":
		return "#9b6b16"
	case "error", "danger":
		return "#a33b3b"
	default:
		return "#2563eb"
	}
}
