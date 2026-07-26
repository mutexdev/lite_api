package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

type WorkspaceWindowOwner struct {
	Version     int       `json:"version"`
	Workspace   string    `json:"workspace"`
	SessionID   string    `json:"sessionId"`
	PID         int       `json:"pid"`
	AcquiredAt  time.Time `json:"acquiredAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
}
type WorkspaceWindowLockStore struct {
	DataDir      string
	Now          func() time.Time
	ProcessAlive func(int) bool
	StaleAfter   time.Duration
}

func NewWorkspaceWindowLockStore(dataDir string) WorkspaceWindowLockStore {
	return WorkspaceWindowLockStore{DataDir: dataDir, Now: time.Now, ProcessAlive: workspaceWindowProcessAlive, StaleAfter: 30 * time.Second}
}

func workspaceWindowProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}
func (s WorkspaceWindowLockStore) lockPath(workspace string) (string, error) {
	workspace, err := canonicalWorkspaceIdentity(workspace)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s.DataDir) == "" {
		return "", errors.New("coordinator data dir is required")
	}
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(filepath.Clean(s.DataDir), "workspace-window-locks", hex.EncodeToString(sum[:])+".json"), nil
}
func canonicalWorkspaceIdentity(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("workspace identity is required")
	}
	if strings.Contains(value, "\x00") {
		return "", errors.New("workspace identity is invalid")
	}
	for _, component := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return "", errors.New("workspace identity traversal is invalid")
		}
	}
	abs, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	// Resolve the deepest existing ancestor as well as an existing final path.
	// EvalSymlinks on the complete path returns ENOENT for a new workspace below
	// a symlinked directory, which would otherwise give the alias a second lock.
	candidate := abs
	var suffix []string
	for {
		physical, evalErr := filepath.EvalSymlinks(candidate)
		if evalErr == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				physical = filepath.Join(physical, suffix[i])
			}
			return filepath.Clean(physical), nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", evalErr
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return filepath.Clean(abs), nil
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = parent
	}
}
func (s WorkspaceWindowLockStore) Acquire(workspace, sessionID string, pid int) (WorkspaceWindowOwner, error) {
	return s.withGuard(workspace, func() (WorkspaceWindowOwner, error) { return s.acquireLocked(workspace, sessionID, pid) })
}
func (s WorkspaceWindowLockStore) Available(workspace string) error {
	_, err := s.withGuard(workspace, func() (WorkspaceWindowOwner, error) {
		path, err := s.lockPath(workspace)
		if err != nil {
			return WorkspaceWindowOwner{}, err
		}
		owner, err := s.read(path)
		if errors.Is(err, os.ErrNotExist) {
			return WorkspaceWindowOwner{}, nil
		}
		if err != nil {
			return WorkspaceWindowOwner{}, err
		}
		if !s.stale(owner, s.now()) {
			return WorkspaceWindowOwner{}, fmt.Errorf("workspace is owned by session %s", owner.SessionID)
		}
		return WorkspaceWindowOwner{}, nil
	})
	return err
}
func (s WorkspaceWindowLockStore) acquireLocked(workspace, sessionID string, pid int) (WorkspaceWindowOwner, error) {
	workspace, err := canonicalWorkspaceIdentity(workspace)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	if strings.TrimSpace(sessionID) == "" || strings.ContainsAny(sessionID, "/\\\x00") {
		return WorkspaceWindowOwner{}, errors.New("window session id is invalid")
	}
	if pid <= 0 {
		return WorkspaceWindowOwner{}, errors.New("owner pid is invalid")
	}
	path, err := s.lockPath(workspace)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	now := s.now()
	if current, err := s.read(path); err == nil {
		if !s.stale(current, now) {
			return WorkspaceWindowOwner{}, fmt.Errorf("workspace is owned by session %s", current.SessionID)
		}
		_ = os.Remove(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return WorkspaceWindowOwner{}, err
	}
	owner := WorkspaceWindowOwner{Version: 1, Workspace: workspace, SessionID: sessionID, PID: pid, AcquiredAt: now, HeartbeatAt: now}
	if err := s.write(path, owner); err != nil {
		return WorkspaceWindowOwner{}, err
	}
	return owner, nil
}
func (s WorkspaceWindowLockStore) Heartbeat(owner WorkspaceWindowOwner) (WorkspaceWindowOwner, error) {
	return s.withGuard(owner.Workspace, func() (WorkspaceWindowOwner, error) { return s.heartbeatLocked(owner) })
}
func (s WorkspaceWindowLockStore) heartbeatLocked(owner WorkspaceWindowOwner) (WorkspaceWindowOwner, error) {
	path, err := s.lockPath(owner.Workspace)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	current, err := s.read(path)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	if !sameWorkspaceWindowOwner(current, owner) {
		return WorkspaceWindowOwner{}, errors.New("workspace owner does not match lock")
	}
	current.HeartbeatAt = s.now()
	if err := s.write(path, current); err != nil {
		return WorkspaceWindowOwner{}, err
	}
	return current, nil
}
func (s WorkspaceWindowLockStore) Release(owner WorkspaceWindowOwner) error {
	_, err := s.withGuard(owner.Workspace, func() (WorkspaceWindowOwner, error) { return WorkspaceWindowOwner{}, s.releaseLocked(owner) })
	return err
}
func (s WorkspaceWindowLockStore) releaseLocked(owner WorkspaceWindowOwner) error {
	path, err := s.lockPath(owner.Workspace)
	if err != nil {
		return err
	}
	current, err := s.read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sameWorkspaceWindowOwner(current, owner) {
		return errors.New("workspace owner does not match lock")
	}
	return os.Remove(path)
}
func (s WorkspaceWindowLockStore) withGuard(workspace string, fn func() (WorkspaceWindowOwner, error)) (WorkspaceWindowOwner, error) {
	path, err := s.lockPath(workspace)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	guard := path + ".guard"
	if err := os.MkdirAll(filepath.Dir(guard), 0o700); err != nil {
		return WorkspaceWindowOwner{}, err
	}
	file, err := os.OpenFile(guard, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	defer func() { _ = file.Close() }()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return WorkspaceWindowOwner{}, err
	}
	// Best-effort: the deferred file close already releases this flock.
	defer func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }()
	return fn()
}
func (s WorkspaceWindowLockStore) read(path string) (WorkspaceWindowOwner, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceWindowOwner{}, err
	}
	var owner WorkspaceWindowOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		return WorkspaceWindowOwner{}, fmt.Errorf("parse workspace owner: %w", err)
	}
	if err := owner.validate(); err != nil {
		return WorkspaceWindowOwner{}, err
	}
	return owner, nil
}
func (s WorkspaceWindowLockStore) write(path string, owner WorkspaceWindowOwner) error {
	if err := owner.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return atomicfile.WritePrivate(path, data)
}
func (s WorkspaceWindowLockStore) stale(owner WorkspaceWindowOwner, now time.Time) bool {
	after := s.StaleAfter
	if after <= 0 {
		after = 30 * time.Second
	}
	return !s.alive(owner.PID) || now.Sub(owner.HeartbeatAt) > after
}
func (s WorkspaceWindowLockStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s WorkspaceWindowLockStore) alive(pid int) bool {
	if s.ProcessAlive != nil {
		return s.ProcessAlive(pid)
	}
	return pid > 0
}
func (o WorkspaceWindowOwner) validate() error {
	if o.Version != 1 {
		return errors.New("workspace owner version is invalid")
	}
	if _, err := canonicalWorkspaceIdentity(o.Workspace); err != nil {
		return err
	}
	if strings.TrimSpace(o.SessionID) == "" || strings.ContainsAny(o.SessionID, "/\\\x00") {
		return errors.New("workspace owner session is invalid")
	}
	if o.PID <= 0 || o.AcquiredAt.IsZero() || o.HeartbeatAt.IsZero() {
		return errors.New("workspace owner is invalid")
	}
	return nil
}
func sameWorkspaceWindowOwner(a, b WorkspaceWindowOwner) bool {
	return a.Workspace == b.Workspace && a.SessionID == b.SessionID && a.PID == b.PID && a.AcquiredAt.Equal(b.AcquiredAt)
}
