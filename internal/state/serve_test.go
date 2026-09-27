package state

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeLock(t *testing.T) {
	s := New(t.TempDir())
	if _, held := s.Served(); held {
		t.Fatal("fresh dir is not served")
	}
	release, err := s.LockServe(Served{PID: 4242, Since: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	// flock is per open file description: a second holder must be another process
	probe := exec.Command(os.Args[0], "-test.run=TestServeLockHelper")
	probe.Env = append(os.Environ(), "FLOK_SERVE_LOCK_HELPER="+s.Dir)
	out, _ := probe.CombinedOutput()
	if !strings.Contains(string(out), "held by 4242") {
		t.Fatalf("second process must see the lock: %s", out)
	}
	if info, held := s.Served(); !held || info.PID != 4242 {
		t.Fatalf("served: %+v %v", info, held)
	}
	release()
	if _, held := s.Served(); held {
		t.Fatal("release must drop the lock")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ServedFile)); !os.IsNotExist(err) {
		t.Fatal("release must remove the served record")
	}
}

func TestServeLockHelper(t *testing.T) {
	dir := os.Getenv("FLOK_SERVE_LOCK_HELPER")
	if dir == "" {
		t.Skip("helper for TestServeLock")
	}
	_, err := New(dir).LockServe(Served{PID: 1})
	if errors.Is(err, ErrServed) {
		fmt.Println("held by 4242:", err) // stdout: the parent test reads it without -test.v
	} else {
		fmt.Println("unexpected:", err)
	}
}
