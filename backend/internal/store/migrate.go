package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func (s *Store) migrate(ctx context.Context) error {
	return s.migrateFS(ctx, migrationFiles)
}

func (s *Store) migrateFS(ctx context.Context, files fs.FS) error {
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("store: list migrations: %w", err)
	}
	if len(names) == 0 {
		return errors.New("store: no migrations embedded")
	}
	scripts := make([]string, len(names))
	for i, name := range names {
		base := path.Base(name)
		if len(base) < 10 || base[4] != '_' {
			return fmt.Errorf("store: invalid migration name %q", name)
		}
		version, err := strconv.Atoi(base[:4])
		if err != nil || version != i+1 {
			return fmt.Errorf("store: migrations must be contiguous from 0001: %q", name)
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("store: read migration %q: %w", name, err)
		}
		scripts[i] = string(body)
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var version, hasHistory int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return fmt.Errorf("store: read schema version: %w", err)
		}
		if version < 0 || version > len(scripts) {
			return fmt.Errorf("store: unsupported schema version %d (latest %d)", version, len(scripts))
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='schema_migrations'").Scan(&hasHistory); err != nil {
			return fmt.Errorf("store: find migration history: %w", err)
		}
		if hasHistory != 0 {
			rows, err := tx.QueryContext(ctx, "SELECT version, applied_at FROM schema_migrations ORDER BY version")
			if err != nil {
				return fmt.Errorf("store: read migration history: %w", err)
			}
			count := 0
			for rows.Next() {
				var v int
				var at int64
				if err := rows.Scan(&v, &at); err != nil {
					return errors.Join(err, rows.Close())
				}
				count++
				if v != count || v > len(scripts) || at <= 0 {
					return errors.Join(fmt.Errorf("store: inconsistent migration history at version %d", v), rows.Close())
				}
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return fmt.Errorf("store: read migration history: %w", err)
			}
			if count != version || count == 0 {
				return fmt.Errorf("store: inconsistent schema version %d and history length %d", version, count)
			}
		} else if version != 0 {
			return errors.New("store: schema version without migration history")
		}
		for i := version; i < len(scripts); i++ {
			if _, err := tx.ExecContext(ctx, scripts[i]); err != nil {
				return fmt.Errorf("store: apply migration %q: %w", names[i], err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", i+1, time.Now().UTC().UnixMilli()); err != nil {
				return fmt.Errorf("store: record migration %q: %w", names[i], err)
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
				return fmt.Errorf("store: update schema version: %w", err)
			}
		}
		return nil
	})
}
