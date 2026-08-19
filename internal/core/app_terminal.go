package core

// The embedded terminal sessions (pty) the DevTools panel drives.
//
// Split out of app.go by AST: declarations are identified by the parser and
// copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/creack/pty"
)

func (a *App) CreateTerminalSession(cwd string) (TerminalSession, error) {
	if a.terminals == nil {
		a.terminals = map[string]*terminalSessionProcess{}
	}
	shell := defaultTerminalShell()
	sessionCWD := normalizeTerminalCWD(cwd, a.dataDir)
	cmd := exec.Command(shell)
	cmd.Dir = sessionCWD
	cmd.Env = os.Environ()
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		return TerminalSession{}, fmt.Errorf("create terminal session: %w", err)
	}
	now := time.Now()
	session := &terminalSessionProcess{
		id:        newID("terminal"),
		cwd:       sessionCWD,
		cmd:       cmd,
		file:      file,
		pid:       cmd.Process.Pid,
		createdAt: now,
		updatedAt: now,
	}
	a.terminalMu.Lock()
	a.terminals[session.id] = session
	snapshot := terminalSessionSnapshotLocked(session)
	a.terminalMu.Unlock()
	go a.readTerminalSession(session.id, file, cmd)
	return snapshot, nil
}

func (a *App) ListTerminalSessions() ([]TerminalSession, error) {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	sessions := make([]TerminalSession, 0, len(a.terminals))
	for _, session := range a.terminals {
		sessions = append(sessions, terminalSessionSnapshotLocked(session))
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt < sessions[j].CreatedAt
	})
	return sessions, nil
}

func (a *App) GetTerminalSession(sessionID string) (TerminalSession, error) {
	a.terminalMu.Lock()
	defer a.terminalMu.Unlock()
	session, ok := a.terminals[sessionID]
	if !ok {
		return TerminalSession{}, errors.New("terminal session not found")
	}
	return terminalSessionSnapshotLocked(session), nil
}

func (a *App) WriteTerminalSession(sessionID, data string) (TerminalSession, error) {
	a.terminalMu.Lock()
	session, ok := a.terminals[sessionID]
	if !ok {
		a.terminalMu.Unlock()
		return TerminalSession{}, errors.New("terminal session not found")
	}
	if session.exited {
		snapshot := terminalSessionSnapshotLocked(session)
		a.terminalMu.Unlock()
		return snapshot, nil
	}
	file := session.file
	a.terminalMu.Unlock()

	if data != "" && file != nil {
		if _, err := file.WriteString(data); err != nil {
			return TerminalSession{}, fmt.Errorf("write terminal session: %w", err)
		}
	}
	return a.GetTerminalSession(sessionID)
}

func (a *App) ResizeTerminalSession(sessionID string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	a.terminalMu.Lock()
	session, ok := a.terminals[sessionID]
	if !ok {
		a.terminalMu.Unlock()
		return errors.New("terminal session not found")
	}
	file := session.file
	exited := session.exited
	a.terminalMu.Unlock()
	if exited || file == nil {
		return nil
	}
	return pty.Setsize(file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (a *App) KillTerminalSession(sessionID string) error {
	a.terminalMu.Lock()
	session, ok := a.terminals[sessionID]
	if !ok {
		a.terminalMu.Unlock()
		return nil
	}
	delete(a.terminals, sessionID)
	a.terminalMu.Unlock()

	if session.file != nil {
		_ = session.file.Close()
	}
	if session.cmd != nil && session.cmd.Process != nil && !session.exited {
		_ = session.cmd.Process.Kill()
	}
	return nil
}

func (a *App) readTerminalSession(sessionID string, file *os.File, cmd *exec.Cmd) {
	buf := make([]byte, 4096)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			a.terminalMu.Lock()
			if session, ok := a.terminals[sessionID]; ok {
				appendTerminalOutputLocked(session, buf[:n])
			}
			a.terminalMu.Unlock()
		}
		if err != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if waitErr != nil {
		exitCode = -1
	}
	a.terminalMu.Lock()
	if session, ok := a.terminals[sessionID]; ok {
		session.exited = true
		session.exitCode = exitCode
		appendTerminalOutputLocked(session, []byte(fmt.Sprintf("\r\n[Process exited with code %d]\r\n", exitCode)))
	}
	a.terminalMu.Unlock()
	_ = file.Close()
}

func defaultTerminalShell() string {
	if goruntime.GOOS == "windows" {
		if shell := strings.TrimSpace(os.Getenv("PWSH")); shell != "" {
			return shell
		}
		return "powershell.exe"
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	return "/bin/bash"
}

func normalizeTerminalCWD(cwd, fallback string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(os.Getenv("HOME"))
	}
	if cwd == "" {
		cwd = strings.TrimSpace(os.Getenv("USERPROFILE"))
	}
	if cwd == "" {
		cwd = fallback
	}
	if info, err := os.Stat(cwd); err == nil && info.IsDir() {
		if abs, err := filepath.Abs(cwd); err == nil {
			return abs
		}
		return cwd
	}
	if info, err := os.Stat(fallback); err == nil && info.IsDir() {
		return fallback
	}
	return "."
}

func appendTerminalOutputLocked(session *terminalSessionProcess, chunk []byte) {
	session.output = append(session.output, chunk...)
	if len(session.output) > terminalOutputLimit {
		next := make([]byte, terminalOutputLimit)
		copy(next, session.output[len(session.output)-terminalOutputLimit:])
		session.output = next
	}
	session.updatedAt = time.Now()
}
