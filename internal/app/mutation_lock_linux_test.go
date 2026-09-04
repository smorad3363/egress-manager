//go:build linux

package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/reliability"
)

func TestWithMutationLockSerializesDifferentComponents(t *testing.T) {
	t.Parallel()

	lock := reliability.FileLock{Path: filepath.Join(t.TempDir(), "operation.lock"), Now: func() time.Time { return time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC) }, PID: 42, ProcessStart: "test-boot:123"}
	lease, err := lock.TryAcquire("nat_apply", "nat")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := withMutationLock(lock, "route_apply", "routing", func() (any, error) {
		called = true
		return nil, nil
	}); !errors.Is(err, reliability.ErrBusy) {
		t.Fatalf("concurrent mutation error = %v, want ErrBusy", err)
	}
	if called {
		t.Fatal("concurrent mutation action ran while the global lock was held")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := withMutationLock(lock, "route_apply", "routing", func() (any, error) {
		called = true
		return "applied", nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("mutation action did not run after lock release")
	}
}
