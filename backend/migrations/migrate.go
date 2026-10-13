// Package migrations applies versioned, checksum-verified database migrations.
package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

// Apply serializes migration runs and applies all missing migrations atomically.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Warn("migration rollback failed")
		}
	}()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(73428101)"); err != nil {
		return fmt.Errorf("lock migration: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	for _, name := range names {
		content, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration: %w", err)
		}
		sum := sha256.Sum256(content)
		checksum := hex.EncodeToString(sum[:])
		var saved string
		err = tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=$1", name).Scan(&saved)
		switch {
		case err == nil:
			if saved != checksum {
				return fmt.Errorf("migration checksum changed: %s", name)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("read migration ledger: %w", err)
		}
		if _, err := tx.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", name, checksum); err != nil {
			return fmt.Errorf("record migration: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// Check verifies that all embedded migrations have been applied unchanged.
func Check(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range names {
		content, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration: %w", err)
		}
		sum := sha256.Sum256(content)
		var saved string
		if err := pool.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=$1", name).Scan(&saved); err != nil {
			return errors.New("database schema is not initialized; run admin migrate")
		}
		if saved != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("migration checksum changed: %s", name)
		}
	}
	return nil
}
