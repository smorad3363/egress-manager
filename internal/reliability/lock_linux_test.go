//go:build linux

package reliability

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestFileLockSerializesMutationsAndPreservesOwnerMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	lock := FileLock{Path: filepath.Join(t.TempDir(), "operation.lock"), Now: func() time.Time { return now }, PID: 42, ProcessStart: "test-boot:123"}
	lease, err := lock.TryAcquire("apply_one", "routing")
	if err != nil {
		t.Fatal(err)
	}
	status, err := lock.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Active || status.Owner == nil || status.Owner.OperationID != domain.ID("apply_one") || status.Owner.Component != "routing" || status.Owner.PID != 42 {
		t.Fatalf("active lock status = %#v", status)
	}
	if _, err := lock.TryAcquire("apply_two", "nat"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquisition error = %v, want ErrBusy", err)
	}
	now = now.Add(time.Minute)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second release = %v", err)
	}
	status, err = lock.Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if status.Active || status.Owner == nil || status.Owner.ReleasedAt == nil || !status.Owner.ReleasedAt.Equal(now) {
		t.Fatalf("released lock status = %#v", status)
	}
	replacement, err := lock.TryAcquire("apply_two", "nat")
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Release(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(lock.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock permissions = %o", info.Mode().Perm())
	}
}

func TestFileLockRejectsUnsafeInputsAndSymlink(t *testing.T) {
	t.Parallel()

	lock := FileLock{Path: "relative.lock", PID: 42, ProcessStart: "test-boot:123"}
	if _, err := lock.TryAcquire("apply", "routing"); err == nil {
		t.Fatal("relative operation lock path accepted")
	}
	lock.Path = filepath.Join(t.TempDir(), "operation.lock")
	if _, err := lock.TryAcquire("INVALID", "routing"); err == nil {
		t.Fatal("invalid operation ID accepted")
	}
	if _, err := lock.TryAcquire("apply", "Routing Unsafe"); err == nil {
		t.Fatal("invalid component accepted")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lock.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.TryAcquire("apply", "routing"); err == nil {
		t.Fatal("symlinked operation lock accepted")
	}
}

func TestFileLockRejectsOversizedInactiveMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "operation.lock")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maximumLockMetadataSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileLock{Path: path}).Inspect(); err == nil {
		t.Fatal("oversized operation lock metadata accepted")
	}
}
