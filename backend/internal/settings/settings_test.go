package settings

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"ups-warden/internal/store"
)

func TestSaveLoadAndInvalidStoredValue(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := Load(t.Context(), db.Read())
	if err != nil || s != Defaults() {
		t.Fatal("空库应返回默认值", s, err)
	}
	s.LogRetentionDays = 30
	s.DeviceDefaults.Overload = 80
	s.Language = "en-US"
	if err = db.WithTx(t.Context(), func(tx *sql.Tx) error { return Save(t.Context(), tx, s) }); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(t.Context(), db.Read())
	if err != nil || loaded != s {
		t.Fatal("往返不一致", loaded, err)
	}
	for _, value := range []string{`"7"`, `null`, `500`} {
		if _, err = db.Write().Exec("UPDATE settings SET value=? WHERE key='detail_retention_days'", value); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(t.Context(), db.Read()); err == nil {
			t.Fatal("无效存储值未报错", value)
		}
	}
}
