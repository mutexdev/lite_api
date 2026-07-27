package workspacestate

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

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/filelock"
)

type WindowOwner struct {
	Version     int       `json:"version"`
	Workspace   string    `json:"workspace"`
	SessionID   string    `json:"sessionId"`
	PID         int       `json:"pid"`
	AcquiredAt  time.Time `json:"acquiredAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
}
type WindowLockStore struct {
	DataDir      string
	Now          func() time.Time
	ProcessAlive func(int) bool
	StaleAfter   time.Duration
}

func NewWindowLockStore(dataDir string) WindowLockStore {
	return WindowLockStore{DataDir: dataDir, Now: time.Now, ProcessAlive: workspaceWindowProcessAlive, StaleAfter: 30 * time.Second}
}

func (s WindowLockStore) lockPath(workspace string) (string, error) {
	workspace, err := CanonicalWorkspaceIdentity(workspace)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s.DataDir) == "" {
		return "", errors.New("coordinator data dir is required")
	}
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(filepath.Clean(s.DataDir), "workspace-window-locks", hex.EncodeToString(sum[:])+".json"), nil
}
func (s WindowLockStore) Acquire(workspace, sessionID string, pid int) (WindowOwner, error) {
	return s.withGuard(workspace, func() (WindowOwner, error) { return s.acquireLocked(workspace, sessionID, pid) })
}
func (s WindowLockStore) Available(workspace string) error {
	_, err := s.withGuard(workspace, func() (WindowOwner, error) {
		path, err := s.lockPath(workspace)
		if err != nil {
			return WindowOwner{}, err
		}
		owner, err := s.read(path)
		if errors.Is(err, os.ErrNotExist) {
			return WindowOwner{}, nil
		}
		if err != nil {
			return WindowOwner{}, err
		}
		if !s.stale(owner, s.now()) {
			return WindowOwner{}, fmt.Errorf("workspace is owned by session %s", owner.SessionID)
		}
		return WindowOwner{}, nil
	})
	return err
}
func (s WindowLockStore) acquireLocked(workspace, sessionID string, pid int) (WindowOwner, error) {
	workspace, err := CanonicalWorkspaceIdentity(workspace)
	if err != nil {
		return WindowOwner{}, err
	}
	if strings.TrimSpace(sessionID) == "" || strings.ContainsAny(sessionID, "/\\\x00") {
		return WindowOwner{}, errors.New("window session id is invalid")
	}
	if pid <= 0 {
		return WindowOwner{}, errors.New("owner pid is invalid")
	}
	path, err := s.lockPath(workspace)
	if err != nil {
		return WindowOwner{}, err
	}
	now := s.now()
	if current, err := s.read(path); err == nil {
		if !s.stale(current, now) {
			return WindowOwner{}, fmt.Errorf("workspace is owned by session %s", current.SessionID)
		}
		_ = os.Remove(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return WindowOwner{}, err
	}
	owner := WindowOwner{Version: 1, Workspace: workspace, SessionID: sessionID, PID: pid, AcquiredAt: now, HeartbeatAt: now}
	if err := s.write(path, owner); err != nil {
		return WindowOwner{}, err
	}
	return owner, nil
}
func (s WindowLockStore) Heartbeat(owner WindowOwner) (WindowOwner, error) {
	return s.withGuard(owner.Workspace, func() (WindowOwner, error) { return s.heartbeatLocked(owner) })
}
func (s WindowLockStore) heartbeatLocked(owner WindowOwner) (WindowOwner, error) {
	path, err := s.lockPath(owner.Workspace)
	if err != nil {
		return WindowOwner{}, err
	}
	current, err := s.read(path)
	if err != nil {
		return WindowOwner{}, err
	}
	if !sameWorkspaceWindowOwner(current, owner) {
		return WindowOwner{}, errors.New("workspace owner does not match lock")
	}
	current.HeartbeatAt = s.now()
	if err := s.write(path, current); err != nil {
		return WindowOwner{}, err
	}
	return current, nil
}
func (s WindowLockStore) Release(owner WindowOwner) error {
	_, err := s.withGuard(owner.Workspace, func() (WindowOwner, error) { return WindowOwner{}, s.releaseLocked(owner) })
	return err
}
func (s WindowLockStore) releaseLocked(owner WindowOwner) error {
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
func (s WindowLockStore) withGuard(workspace string, fn func() (WindowOwner, error)) (WindowOwner, error) {
	path, err := s.lockPath(workspace)
	if err != nil {
		return WindowOwner{}, err
	}
	guard := path + ".guard"
	if err := os.MkdirAll(filepath.Dir(guard), 0o700); err != nil {
		return WindowOwner{}, err
	}
	file, err := os.OpenFile(guard, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return WindowOwner{}, err
	}
	defer func() { _ = file.Close() }()
	unlockFile, err := filelock.Exclusive(file)
	if err != nil {
		return WindowOwner{}, err
	}
	// Best-effort: the deferred file close already releases this lock.
	defer unlockFile()
	return fn()
}
func (s WindowLockStore) read(path string) (WindowOwner, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WindowOwner{}, err
	}
	var owner WindowOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		return WindowOwner{}, fmt.Errorf("parse workspace owner: %w", err)
	}
	if err := owner.validate(); err != nil {
		return WindowOwner{}, err
	}
	return owner, nil
}
func (s WindowLockStore) write(path string, owner WindowOwner) error {
	if err := owner.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return atomicfile.WritePrivate(path, data)
}
func (s WindowLockStore) stale(owner WindowOwner, now time.Time) bool {
	after := s.StaleAfter
	if after <= 0 {
		after = 30 * time.Second
	}
	return !s.alive(owner.PID) || now.Sub(owner.HeartbeatAt) > after
}
func (s WindowLockStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s WindowLockStore) alive(pid int) bool {
	if s.ProcessAlive != nil {
		return s.ProcessAlive(pid)
	}
	return pid > 0
}
func (o WindowOwner) validate() error {
	if o.Version != 1 {
		return errors.New("workspace owner version is invalid")
	}
	if _, err := CanonicalWorkspaceIdentity(o.Workspace); err != nil {
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
func sameWorkspaceWindowOwner(a, b WindowOwner) bool {
	return a.Workspace == b.Workspace && a.SessionID == b.SessionID && a.PID == b.PID && a.AcquiredAt.Equal(b.AcquiredAt)
}
