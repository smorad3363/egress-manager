package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/egress-manager/egress-manager/internal/auth"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
)

func ProvisionAdmin(ctx context.Context, configPath, keyPath, username, password string) error {
	configuration, err := config.Load(configPath)
	if err != nil {
		return err
	}
	key, err := config.LoadSharedKey(keyPath)
	if err != nil {
		return err
	}
	databaseConnection, err := database.Open(ctx, configuration.DatabasePath)
	if err != nil {
		return err
	}
	defer databaseConnection.Close()
	store := database.NewStore(databaseConnection)
	sessions, err := auth.NewSessionManager(store, 24*time.Hour)
	if err != nil {
		return err
	}
	bucketKey, err := deriveAuthenticationKey(key)
	if err != nil {
		return err
	}
	service, err := auth.NewService(store, auth.NewPasswordHasher(), sessions, auth.DefaultThrottlePolicy(), bucketKey)
	if err != nil {
		return err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return fmt.Errorf("generate admin ID: %w", err)
	}
	if err := service.ProvisionAdmin(ctx, domain.ID("admin_"+hex.EncodeToString(idBytes)), username, password, time.Now().UTC()); err != nil {
		return err
	}
	if err := databaseConnection.Close(); err != nil {
		return fmt.Errorf("close provisioning database: %w", err)
	}
	if err := repairProvisionDatabasePermissions(configuration.DatabasePath); err != nil {
		return err
	}
	return nil
}

// repairProvisionDatabasePermissions keeps SQLite WAL/SHM files writable by the
// service group when provision-admin is invoked as root while egress-web is active.
// The database file's group is the source of truth, so custom database paths retain
// their configured ownership boundary.
func repairProvisionDatabasePermissions(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	databaseInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect provisioned database permissions: %w", err)
	}
	stat, ok := databaseInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect provisioned database group: unsupported stat metadata")
	}
	groupID := int(stat.Gid)

	directory := filepath.Dir(path)
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("inspect database directory permissions: %w", err)
	}
	if err := os.Chown(directory, -1, groupID); err != nil {
		return fmt.Errorf("set database directory group: %w", err)
	}
	directoryMode := directoryInfo.Mode().Perm() | 0o070 | os.ModeSetgid
	if err := os.Chmod(directory, directoryMode); err != nil {
		return fmt.Errorf("set database directory group inheritance: %w", err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		info, err := os.Stat(sidecar)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite sidecar %s: %w", suffix, err)
		}
		if err := os.Chown(sidecar, -1, groupID); err != nil {
			return fmt.Errorf("set SQLite sidecar group %s: %w", suffix, err)
		}
		if err := os.Chmod(sidecar, info.Mode().Perm()|0o060); err != nil {
			return fmt.Errorf("set SQLite sidecar group permissions %s: %w", suffix, err)
		}
	}
	return nil
}
