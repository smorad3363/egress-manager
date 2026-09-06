//go:build !windows

package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAllowsOnlyOwnerAndServiceGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database", "egress-manager.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o660 {
		t.Fatalf("database mode = %04o, want 0660", got)
	}
}

func TestOpenRejectsDatabaseSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	path := filepath.Join(directory, "egress-manager.db")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if database, err := Open(context.Background(), path); err == nil {
		_ = database.Close()
		t.Fatal("Open() error = nil, want symlink rejection")
	}
}
