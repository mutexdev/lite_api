package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const windowSessionVersion = 1

type WindowGeometry struct {
	X      int `json:"x,omitempty"`
	Y      int `json:"y,omitempty"`
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}
type WindowSession struct {
	Version                 int            `json:"version"`
	ID                      string         `json:"id"`
	WorkspaceID             string         `json:"workspaceId,omitempty"`
	WorkspacePath           string         `json:"workspacePath,omitempty"`
	OpenTabs                []OpenTab      `json:"openTabs"`
	ClosedTabs              []OpenTab      `json:"closedTabs,omitempty"`
	ActiveTabID             string         `json:"activeTabId,omitempty"`
	ResponsePaneOrientation string         `json:"responsePaneOrientation,omitempty"`
	Geometry                WindowGeometry `json:"geometry,omitempty"`
	UpdatedAt               time.Time      `json:"updatedAt"`
}
type WindowLaunchIntent struct {
	SessionID     string
	WorkspaceID   string
	WorkspacePath string
	DataDir       string
}

func ParseWindowLaunchIntent(args []string) (WindowLaunchIntent, error) {
	var intent WindowLaunchIntent
	for i := 0; i < len(args); i++ {
		key := args[i]
		if key != "--window-session" && key != "--workspace-id" && key != "--workspace-path" && key != "--data-dir" {
			continue
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return WindowLaunchIntent{}, fmt.Errorf("%s requires a value", key)
		}
		value := strings.TrimSpace(args[i+1])
		i++
		if value == "" {
			return WindowLaunchIntent{}, fmt.Errorf("%s requires a value", key)
		}
		switch key {
		case "--window-session":
			if intent.SessionID != "" {
				return WindowLaunchIntent{}, errors.New("window session specified more than once")
			}
			intent.SessionID = value
		case "--workspace-id":
			if intent.WorkspaceID != "" {
				return WindowLaunchIntent{}, errors.New("workspace id specified more than once")
			}
			intent.WorkspaceID = value
		case "--workspace-path":
			if intent.WorkspacePath != "" {
				return WindowLaunchIntent{}, errors.New("workspace path specified more than once")
			}
			intent.WorkspacePath = filepath.Clean(value)
		case "--data-dir":
			if intent.DataDir != "" {
				return WindowLaunchIntent{}, errors.New("data dir specified more than once")
			}
			intent.DataDir = filepath.Clean(value)
		}
	}
	if intent.SessionID == "" {
		return WindowLaunchIntent{}, errors.New("window session is required")
	}
	if intent.WorkspaceID != "" && intent.WorkspacePath != "" {
		return WindowLaunchIntent{}, errors.New("workspace id and workspace path are mutually exclusive")
	}
	if intent.WorkspaceID == "" && intent.WorkspacePath == "" {
		return WindowLaunchIntent{}, errors.New("workspace id or workspace path is required")
	}
	if intent.DataDir == "" {
		return WindowLaunchIntent{}, errors.New("data dir is required")
	}
	return intent, nil
}
func (s WindowSession) Validate() error {
	if s.Version != windowSessionVersion {
		return fmt.Errorf("unsupported window session version %d", s.Version)
	}
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("window session id is required")
	}
	if s.WorkspaceID != "" && s.WorkspacePath != "" {
		return errors.New("window session has conflicting workspace identity")
	}
	if s.WorkspaceID == "" && s.WorkspacePath == "" {
		return errors.New("window session workspace is required")
	}
	if s.ResponsePaneOrientation != "" && s.ResponsePaneOrientation != "horizontal" && s.ResponsePaneOrientation != "vertical" {
		return errors.New("response pane orientation is invalid")
	}
	g := s.Geometry
	if (g.Width == 0 && g.Height == 0 && (g.X != 0 || g.Y != 0)) || (g.Width == 0) != (g.Height == 0) || g.Width < 0 || g.Height < 0 || g.Width > 10000 || g.Height > 10000 || (g.Width > 0 && (g.Width < 320 || g.Height < 240)) {
		return errors.New("window geometry is invalid")
	}
	return nil
}
func WriteWindowSession(path string, session WindowSession) error {
	if err := session.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateAtomic(filepath.Clean(path), data)
}
func ReadWindowSession(path string) (WindowSession, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return WindowSession{}, err
	}
	var session WindowSession
	if err := json.Unmarshal(data, &session); err != nil {
		return WindowSession{}, fmt.Errorf("parse window session: %w", err)
	}
	if err := session.Validate(); err != nil {
		return WindowSession{}, err
	}
	return session, nil
}
func MigrateDefaultWindowSession(sessionID, workspaceID, workspacePath string, state AppState) (WindowSession, error) {
	workspaceID, workspacePath = strings.TrimSpace(workspaceID), strings.TrimSpace(workspacePath)
	var selected *Workspace
	for i := range state.Workspaces {
		ws := &state.Workspaces[i]
		if (workspaceID != "" && ws.ID == workspaceID) || (workspacePath != "" && filepath.Clean(ws.Path) == filepath.Clean(workspacePath)) {
			selected = ws
			break
		}
	}
	allowed := map[string]bool{}
	if selected != nil {
		workspaceID, workspacePath = selected.ID, ""
		for _, collection := range selected.Collections {
			allowed[collection.ID] = true
		}
	}
	filter := func(tabs []OpenTab) []OpenTab {
		out := []OpenTab{}
		for _, tab := range tabs {
			if allowed[tab.CollectionID] {
				out = append(out, tab)
			}
		}
		return out
	}
	open := filter(state.OpenTabs)
	active := state.ActiveTabID
	present := false
	for _, tab := range open {
		if tab.ID == active {
			present = true
		}
	}
	if !present {
		active = ""
		if len(open) > 0 {
			active = open[0].ID
		}
	}
	s := WindowSession{Version: windowSessionVersion, ID: strings.TrimSpace(sessionID), WorkspaceID: workspaceID, WorkspacePath: workspacePath, OpenTabs: open, ClosedTabs: filter(state.ClosedTabs), ActiveTabID: active, ResponsePaneOrientation: state.Preferences.Layout.ResponsePaneOrientation, UpdatedAt: time.Now().UTC()}
	return s, s.Validate()
}
