package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strconv"
	"strings"
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
		// 重放已应用脚本到独立内存库，拒绝缺表、改列、丢失索引或外部 DDL。
		if err := validateSchema(ctx, tx, scripts[:version]); err != nil {
			return err
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

type schemaQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func schema(ctx context.Context, db schemaQuerier) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT type, name, sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			return nil, err
		}
		result[kind+":"+name] = strings.TrimSpace(ddl)
	}
	return result, rows.Err()
}

func validateSchema(ctx context.Context, tx *sql.Tx, scripts []string) (err error) {
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return fmt.Errorf("store: open schema reference: %w", err)
	}
	reference.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, reference.Close()) }()
	for _, script := range scripts {
		if _, err := reference.ExecContext(ctx, script); err != nil {
			return fmt.Errorf("store: build schema reference: %w", err)
		}
	}
	want, err := schema(ctx, reference)
	if err != nil {
		return fmt.Errorf("store: read schema reference: %w", err)
	}
	got, err := schema(ctx, tx)
	if err != nil {
		return fmt.Errorf("store: inspect schema: %w", err)
	}
	if !maps.Equal(got, want) {
		return errors.New("store: inconsistent schema: database DDL differs from migration history")
	}
	return nil
}
