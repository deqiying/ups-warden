// Package store 提供 SQLite 连接池、前向迁移和写事务基础设施。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Store 分离只读连接池和串行写连接；调用方不得更改连接级 PRAGMA。
type Store struct {
	read  *sql.DB
	write *sql.DB
}

// Open 打开文件数据库并完成迁移；path 是文件路径而非 SQLite DSN。
// 不接受内存数据库，不负责创建父目录；失败时释放已打开的资源。
func Open(ctx context.Context, path string) (_ *Store, err error) {
	if path == "" || path == ":memory:" || strings.ContainsRune(path, '\x00') {
		return nil, errors.New("store: require a nonempty database file path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve path: %w", err)
	}
	s := &Store{}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	s.write, err = sql.Open("sqlite", fileURI(abs, false))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	s.write.SetMaxOpenConns(1)
	s.write.SetMaxIdleConns(1)
	var mode string
	if err = s.write.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return nil, fmt.Errorf("store: enable WAL: %w", err)
	}
	if mode != "wal" {
		return nil, fmt.Errorf("store: expected WAL, got %q", mode)
	}
	if err = s.migrate(ctx); err != nil {
		return nil, err
	}
	s.read, err = sql.Open("sqlite", fileURI(abs, true))
	if err != nil {
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	s.read.SetMaxOpenConns(4)
	s.read.SetMaxIdleConns(4)
	if err = s.read.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("store: ping reader: %w", err)
	}
	return s, nil
}

func fileURI(path string, readonly bool) string {
	path = filepath.ToSlash(path)
	// Windows 盘符必须使用 file:///C:/...；URL 编码防止文件名成为查询参数。
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	q := url.Values{}
	q.Set("mode", "rwc")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "synchronous=FULL")
	if readonly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only=ON")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

// Read 返回最多四条连接的只读池；即使关闭 query_only，文件仍以 mode=ro 打开。
func (s *Store) Read() *sql.DB { return s.read }

// Write 返回单连接写池；需要原子性时使用 WithTx。
func (s *Store) Write() *sql.DB { return s.write }

// WithTx 在写池执行事务，回调失败时回滚并保留原始错误。
// panic 会先回滚再原样抛出；提交失败和回滚失败不会被隐藏。
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("store: rollback transaction: %w", rollbackErr))
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}

// Close 关闭两个连接池，并合并各自的关闭错误；可重复调用。
func (s *Store) Close() error {
	var errs []error
	if s.read != nil {
		errs = append(errs, s.read.Close())
	}
	if s.write != nil {
		errs = append(errs, s.write.Close())
	}
	return errors.Join(errs...)
}
