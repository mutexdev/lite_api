package core

// The notification centre: raising them and marking them read.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"fmt"
	"strings"
	"time"
)

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
	a.state.Notifications = append([]Notification{{
		ID:          newID("notification"),
		Level:       level,
		Type:        notificationType(level),
		Title:       notificationTitle(level, message),
		Message:     message,
		Description: message,
		Color:       notificationColor(level),
		At:          time.Now(),
	}}, a.state.Notifications...)
	if len(a.state.Notifications) > 20 {
		a.state.Notifications = a.state.Notifications[:20]
	}
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
