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
