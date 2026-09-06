// Package database owns SQLite initialization, migrations, and repositories.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	maximumOpenConnections = 4
	maximumIdleConnections = 2
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("database path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	databaseURL := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := databaseURL.Query()
	query.Set("_busy_timeout", "5000")
	query.Set("_foreign_keys", "on")
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "NORMAL")
	databaseURL.RawQuery = query.Encode()

	database, err := sql.Open("sqlite3", databaseURL.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	database.SetMaxOpenConns(maximumOpenConnections)
	database.SetMaxIdleConns(maximumIdleConnections)
	database.SetConnMaxIdleTime(5 * time.Minute)

	closeOnError := func(openErr error) (*sql.DB, error) {
		_ = database.Close()
		return nil, openErr
	}
	if err := database.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping SQLite: %w", err))
	}
	// egressd and the unprivileged egress-web process share the database through
	// a dedicated operating-system group. The containing directory remains the
	// access boundary; no permissions are granted to other users.
	if err := os.Chmod(path, 0o660); err != nil {
		return closeOnError(fmt.Errorf("set database permissions: %w", err))
	}
	if err := Migrate(ctx, database); err != nil {
		return closeOnError(err)
	}
	return database, nil
}
