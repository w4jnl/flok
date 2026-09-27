package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// ServeLockFile is held (flock) by `flok serve` while a local flok drives this state dir
	// over ssh; a crashed serve releases it by itself.
	ServeLockFile = "serve.lock"
	// ServedFile records who holds the lock, for `flok doctor` and `flok status` on the host.
	ServedFile = "served"
)

// Served describes the serve session holding the lock.
type Served struct {
	PID       int       `json:"pid"`
	Since     time.Time `json:"since"`
	SSHClient string    `json:"ssh_client,omitempty"` // $SSH_CONNECTION of the serve process
}

// ErrServed is returned by LockServe when another serve holds the lock.
var ErrServed = errors.New("already served")

// LockServe takes the exclusive serve lock and writes the served record; release removes the
// record and drops the lock. A held lock returns ErrServed with the holder's pid.
func (s *Store) LockServe(info Served) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(s.Dir, ServeLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if holder, ok := s.Served(); ok && holder.PID > 0 {
			return nil, fmt.Errorf("%w (pid %d)", ErrServed, holder.PID)
		}
		return nil, ErrServed
	}
	if err := writeJSON(filepath.Join(s.Dir, ServedFile), info); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	return func() {
		_ = os.Remove(filepath.Join(s.Dir, ServedFile))
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Served reports whether a serve session holds this state dir (a non-blocking lock probe, so
// the hook can ask on every event) and, when readable, who.
func (s *Store) Served() (Served, bool) {
	f, err := os.OpenFile(filepath.Join(s.Dir, ServeLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Served{}, false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return Served{}, false
	}
	var info Served
	if data, err := os.ReadFile(filepath.Join(s.Dir, ServedFile)); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	return info, true
}
