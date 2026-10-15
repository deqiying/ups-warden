package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "warden # % & 中文.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func execTest(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestSchemaAndIdempotentMigration(t *testing.T) {
	s := openTest(t)
	tables := map[string]string{
		"schema_migrations": "version applied_at",
		"settings":          "key value",
		"users":             "id username password_hash role created_at last_login_at",
		"sessions":          "token_hash user_id csrf_token created_at expires_at last_seen_at ip user_agent",
		"devices":           "id name note location tags enabled config",
		"sources":           "id device_id protocol host port params secret profile_key profile_version poll_ms timeout_ms retries priority enabled",
		"profiles":          "key name protocol version content created_at updated_at",
		"device_state":      "device_id power_state power_state_raw pending_since on_battery_since outage_cycle_id comm_state last_ok_at alert_flags updated_at",
		"outage_cycles":     "id device_id started_at ended_at",
		"events":            "id device_id type severity started_at cleared_at active summary acked_at acked_by",
		"history_sample":    "device_id ts data",
		"history_1m":        "device_id bucket data",
		"rules":             "id name enabled dry_run scope trigger actions recovery_actions cooldown_s",
		"rule_state":        "rule_id device_id fired fired_at since",
		"rule_logs":         "id rule_id device_id at kind reason values action_run_ids",
		"channels":          "id name type config secret enabled",
		"notify_templates":  "event_type format body",
		"notify_logs":       "id channel_id event_id at summary ok duration_ms error target attempts template_fallback",
		"hosts":             "id name address kind conn secret hostkey_fingerprint online_check",
		"actions":           "id name type config secret timeout_s",
		"action_runs":       "id action_id trigger dry_run status started_at ended_at exit_code stdout stderr error",
		"plans":             "id name config",
		"plan_runs":         "id plan_id device_id outage_cycle_id dry_run status trigger countdown_until current_stage plan_snapshot started_at ended_at",
		"plan_run_steps":    "run_id stage step action_id host_id status started_at ended_at result",
		"audit_logs":        "id at user_id ip kind target diff result",
	}
	for table, columns := range tables {
		rows, err := s.Read().QueryContext(t.Context(), "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			got = append(got, name)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, " ") != columns {
			t.Errorf("table %s columns: %v, want %s", table, got, columns)
		}
	}
	var count int
	if err := s.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); err != nil || count != len(tables) {
		t.Fatalf("table count %d: %v", count, err)
	}
	var before, after int64
	if err := s.Read().QueryRowContext(t.Context(), "SELECT applied_at FROM schema_migrations WHERE version=1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if delta := time.Now().UTC().UnixMilli() - before; delta < 0 || delta > 60000 {
		t.Fatalf("migration timestamp is not current Unix milliseconds: %d", before)
	}
	execTest(t, s.Write(), "INSERT INTO settings VALUES ('test', '{}')")
	if err := s.migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Read().QueryRowContext(t.Context(), "SELECT applied_at FROM schema_migrations WHERE version=1").Scan(&after); err != nil || after != before {
		t.Fatalf("migration was reapplied: %d -> %d: %v", before, after, err)
	}
}

func TestConnectionPragmasAndReadonly(t *testing.T) {
	s := openTest(t)
	if s.Read().Stats().MaxOpenConnections != 4 || s.Write().Stats().MaxOpenConnections != 1 {
		t.Fatal("incorrect pool sizes")
	}
	check := func(db *sql.DB, n int, readonly bool) {
		// 同时持有连接，确保断言检查的是不同物理连接。
		var conns []*sql.Conn
		defer func() {
			for _, conn := range conns {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}
		}()
		for i := 0; i < n; i++ {
			conn, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, conn)
			for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2} {
				var got int
				if err := conn.QueryRowContext(t.Context(), "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
					t.Fatalf("connection %d PRAGMA %s=%d, want %d: %v", i, pragma, got, want, err)
				}
			}
			var mode string
			if err := conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
				t.Fatalf("journal=%s: %v", mode, err)
			}
			if readonly {
				var queryOnly int
				if err := conn.QueryRowContext(t.Context(), "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
					t.Fatalf("query_only=%d: %v", queryOnly, err)
				}
				if _, err := conn.ExecContext(t.Context(), "PRAGMA query_only=OFF"); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(t.Context(), "INSERT INTO settings VALUES ('forbidden', '{}')"); err == nil {
					t.Fatal("reader allowed write after query_only=OFF")
				}
			}
		}
	}
	check(s.Read(), 4, true)
	check(s.Write(), 1, false)
	// 强制销毁连接，验证 DSN 的 PRAGMA 对后续新连接同样生效。
	s.Read().SetMaxIdleConns(0)
	s.Write().SetMaxIdleConns(0)
	check(s.Read(), 4, true)
	check(s.Write(), 1, false)
}

func TestWithTx(t *testing.T) {
	s := openTest(t)
	sentinel := errors.New("callback failed")
	err := s.WithTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO settings VALUES ('rollback', '{}')"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost callback error: %v", err)
	}
	func() {
		defer func() {
			if got := recover(); got != sentinel {
				t.Fatalf("panic changed: %v", got)
			}
		}()
		_ = s.WithTx(t.Context(), func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO settings VALUES ('panic', '{}')"); err != nil {
				t.Fatal(err)
			}
			panic(sentinel)
		})
	}()
	var count int
	if err := s.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM settings").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left %d rows: %v", count, err)
	}
	if err := s.WithTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "INSERT INTO settings VALUES ('commit', '{}')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM settings").Scan(&count); err != nil || count != 1 {
		t.Fatalf("commit left %d rows: %v", count, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WithTx(ctx, func(*sql.Tx) error { t.Fatal("cancelled callback ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestForeignKeysAndChecks(t *testing.T) {
	s := openTest(t)
	for _, query := range []string{
		"INSERT INTO sessions(token_hash,user_id,csrf_token,created_at,expires_at,last_seen_at) VALUES ('x',999,'c',0,1,0)",
		"INSERT INTO devices(name, enabled) VALUES ('invalid', 2)",
		"INSERT INTO devices(name, config) VALUES ('invalid', 'not json')",
		"INSERT INTO settings VALUES ('invalid', 'not json')",
		"INSERT INTO outage_cycles(device_id,started_at) VALUES (999,0)",
	} {
		if _, err := s.Write().ExecContext(t.Context(), query); err == nil {
			t.Errorf("expected constraint rejection: %s", query)
		}
	}
	execTest(t, s.Write(), "INSERT INTO users(id,username,password_hash,created_at) VALUES (1,'admin','hash',0)")
	execTest(t, s.Write(), "INSERT INTO sessions(token_hash,user_id,csrf_token,created_at,expires_at,last_seen_at) VALUES ('x',1,'c',0,1,0)")
	execTest(t, s.Write(), "DELETE FROM users WHERE id=1")
	var count int
	if err := s.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("session cascade failed: %d: %v", count, err)
	}
	if err := s.WithTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO devices(id,name) VALUES (1,'rollback')"); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), "INSERT INTO outage_cycles(device_id,started_at) VALUES (999,0)")
		return err
	}); err == nil {
		t.Fatal("foreign key did not fail transaction")
	}
	if err := s.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM devices").Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign key rollback failed: %d: %v", count, err)
	}
}

func TestPlanRunConstraints(t *testing.T) {
	s := openTest(t)
	execTest(t, s.Write(), "INSERT INTO plans(id,name,config) VALUES (1,'p','{}'), (2,'p2','{}')")
	execTest(t, s.Write(), "INSERT INTO devices(id,name) VALUES (1,'d'),(2,'other')")
	execTest(t, s.Write(), "INSERT INTO outage_cycles(id,device_id,started_at) VALUES (1,1,0),(2,1,10)")
	insert := func(plan, dry int, cycle any, status string, wantOK bool) {
		t.Helper()
		_, err := s.Write().ExecContext(t.Context(), "INSERT INTO plan_runs(plan_id,device_id,outage_cycle_id,dry_run,status,trigger,plan_snapshot,started_at) VALUES (?,1,?,?,?,'{}','{}',0)", plan, cycle, dry, status)
		if (err == nil) != wantOK {
			t.Fatalf("plan=%d dry=%d cycle=%v status=%s: %v", plan, dry, cycle, status, err)
		}
	}
	insert(1, 0, 1, "completed", true)
	insert(1, 0, 1, "cancelled", false)
	insert(1, 1, 1, "running", true)
	insert(1, 1, 1, "running", false)
	insert(1, 0, 2, "countdown", false)
	execTest(t, s.Write(), "UPDATE plan_runs SET status='completed' WHERE plan_id=1 AND dry_run=1")
	insert(1, 0, 2, "countdown", true)
	insert(1, 1, 2, "countdown", false)
	insert(1, 0, nil, "running", false)
	insert(2, 0, nil, "running", true)
	execTest(t, s.Write(), "UPDATE plan_runs SET status='completed' WHERE plan_id=1 AND dry_run=0 AND status='countdown'")
	insert(1, 0, nil, "running", true)
	execTest(t, s.Write(), "UPDATE plan_runs SET status='completed' WHERE plan_id=1 AND dry_run=0 AND status='running'")
	insert(1, 0, nil, "running", true)
	if _, err := s.Write().ExecContext(t.Context(), "INSERT INTO plan_runs(plan_id,device_id,outage_cycle_id,dry_run,status,trigger,plan_snapshot,started_at) VALUES (2,2,1,1,'completed','{}','{}',0)"); err == nil {
		t.Fatal("accepted outage cycle belonging to another device")
	}
}

func TestRejectInconsistentMigrationHistory(t *testing.T) {
	for _, mutation := range []string{
		"PRAGMA user_version=3",
		"PRAGMA user_version=0",
		"DELETE FROM schema_migrations",
		"INSERT INTO schema_migrations VALUES (3,1)",
		"UPDATE schema_migrations SET applied_at=0",
		"DROP TABLE schema_migrations",
	} {
		t.Run(mutation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db.sqlite")
			s, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			execTest(t, s.Write(), mutation)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if got, err := Open(t.Context(), path); err == nil || got != nil {
				if got != nil {
					_ = got.Close()
				}
				t.Fatalf("accepted inconsistent database: %v", err)
			}
			// Windows 删除成功也证明初始化失败没有遗留文件句柄。
			if err := os.Remove(path); err != nil {
				t.Fatalf("database resource leaked: %v", err)
			}
		})
	}
}

func TestMigrationAllowsAdditionalIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	execTest(t, s.Write(), "CREATE INDEX diagnostic_settings ON settings(value)")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationOrderAndAtomicFailure(t *testing.T) {
	s := openTest(t)
	body, err := migrationFiles.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	notifyBody, err := migrationFiles.ReadFile("migrations/0002_notify_results.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"migrations/0001_init.sql":           {Data: body},
		"migrations/0002_notify_results.sql": {Data: notifyBody},
		"migrations/0003_test.sql":           {Data: []byte("CREATE TABLE next_table (id INTEGER PRIMARY KEY);")},
		"migrations/0004_bad.sql":            {Data: []byte("CREATE TABLE partial (id INTEGER); INVALID SQL;")},
	}
	if err := s.migrateFS(t.Context(), files); err == nil || !strings.Contains(err.Error(), "0004_bad.sql") {
		t.Fatalf("bad migration error: %v", err)
	}
	var version, count int
	if err := s.Read().QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("version not rolled back: %d: %v", version, err)
	}
	if err := s.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name IN ('next_table','partial')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("DDL not rolled back: %d: %v", count, err)
	}
	delete(files, "migrations/0004_bad.sql")
	if err := s.migrateFS(t.Context(), files); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateFS(t.Context(), files); err != nil {
		t.Fatalf("incremental migration not idempotent: %v", err)
	}
	delete(files, "migrations/0003_test.sql")
	files["migrations/0004_gap.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	if err := s.migrateFS(t.Context(), files); err == nil {
		t.Fatal("accepted migration numbering gap")
	}
}

func TestOpenPathsAndClose(t *testing.T) {
	for _, path := range []string{"", ":memory:", "bad\x00path", filepath.Join(t.TempDir(), "missing", "db")} {
		if s, err := Open(t.Context(), path); err == nil || s != nil {
			t.Fatalf("accepted invalid path %q: %v", path, err)
		}
	}
	path := filepath.Join(t.TempDir(), "reopen.sqlite")
	for i := 0; i < 2; i++ {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Read().PingContext(t.Context()); err == nil {
			t.Fatal("reader still open")
		}
		if err := s.Write().PingContext(t.Context()); err == nil {
			t.Fatal("writer still open")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if s, err := Open(ctx, filepath.Join(t.TempDir(), "cancelled.sqlite")); s != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open: %v", err)
	}
	// URI 参数不能由文件名注入；Windows 路径中的盘符、空格和百分号保留。
	uri := fileURI("C:/data/a # % & 中文.sqlite", true)
	if !strings.HasPrefix(uri, "file:///C:/") || !strings.Contains(uri, "mode=ro") || strings.Contains(uri, " # ") {
		t.Fatal(fmt.Sprintf("unsafe Windows URI: %s", uri))
	}
}
