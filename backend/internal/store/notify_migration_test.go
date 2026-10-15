package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestNotifyMigrationPreservesM4Logs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m4.db")
	db, err := sql.Open("sqlite", fileURI(path, false))
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrationFiles.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	execTest(t, db, string(body))
	execTest(t, db, "INSERT INTO schema_migrations VALUES(1,1)")
	execTest(t, db, "PRAGMA user_version=1")
	execTest(t, db, "INSERT INTO channels(id,name,type,config) VALUES(1,'fixture','webhook','{}')")
	execTest(t, db, "INSERT INTO notify_logs(id,channel_id,at,summary,ok,duration_ms) VALUES(1,1,1,'旧通知',1,10)")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var summary, target string
	var attempts, fallback int
	if err := s.Read().QueryRow("SELECT summary,target,attempts,template_fallback FROM notify_logs WHERE id=1").Scan(&summary, &target, &attempts, &fallback); err != nil {
		t.Fatal(err)
	}
	if summary != "旧通知" || target != "" || attempts != 1 || fallback != 0 {
		t.Fatal(summary, target, attempts, fallback)
	}
	if err := s.migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
}
