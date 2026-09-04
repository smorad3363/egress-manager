package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationNamePattern = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		matches := migrationNamePattern.FindStringSubmatch(name)
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q", name)
		}
		version, err := strconv.Atoi(matches[1])
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", name, err)
		}
		contents, err := migrationFiles.ReadFile(filepath.ToSlash(filepath.Join("migrations", name)))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, migration{
			Version:  version,
			Name:     name,
			SQL:      string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for index := 1; index < len(migrations); index++ {
		if migrations[index-1].Version == migrations[index].Version {
			return nil, fmt.Errorf("duplicate migration version %04d", migrations[index].Version)
		}
	}
	return migrations, nil
}

func Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS schema_migrations (
            version INTEGER PRIMARY KEY,
            name TEXT NOT NULL UNIQUE,
            checksum TEXT NOT NULL,
            applied_at INTEGER NOT NULL
        ) STRICT
    `); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, candidate := range migrations {
		var checksum string
		err := database.QueryRowContext(ctx,
			`SELECT checksum FROM schema_migrations WHERE version = ?`,
			candidate.Version,
		).Scan(&checksum)
		switch {
		case err == nil:
			if checksum != candidate.Checksum {
				return fmt.Errorf("migration %04d checksum mismatch", candidate.Version)
			}
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read migration %04d state: %w", candidate.Version, err)
		}

		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %04d: %w", candidate.Version, err)
		}
		if _, err := transaction.ExecContext(ctx, candidate.SQL); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("apply migration %04d: %w", candidate.Version, err)
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
			candidate.Version,
			candidate.Name,
			candidate.Checksum,
			time.Now().UTC().Unix(),
		); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("record migration %04d: %w", candidate.Version, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit migration %04d: %w", candidate.Version, err)
		}
	}
	return nil
}
