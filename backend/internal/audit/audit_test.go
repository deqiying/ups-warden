package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"ups-warden/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "audit.sqlite"))
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
func TestSensitiveDiffAndRollback(t *testing.T) {
	s := testStore(t)
	r := Record{Kind: "update", Target: "test", Result: "success", Before: map[string]any{"unchanged": 1, "password": "old-pass", "nested": map[string]any{"name": "old"}}, After: map[string]any{
		"unchanged": 1, "password": "raw-pass", "nested": map[string]any{"name": "new", "Authorization": "raw-auth", "extra": "raw-extra"},
		"list": []any{map[string]any{"community": "raw-community", "public": true}}, "headers": map[string]any{"Custom": "raw-header"}, "private_key": "raw-key",
	}, Sensitive: []string{"nested.extra"}}
	if err := s.WithTx(t.Context(), func(tx *sql.Tx) error { return Write(t.Context(), tx, r) }); err != nil {
		t.Fatal(err)
	}
	var diff string
	var user sql.NullInt64
	if err := s.Read().QueryRowContext(t.Context(), `SELECT diff,user_id FROM audit_logs`).Scan(&diff, &user); err != nil {
		t.Fatal(err)
	}
	if user.Valid {
		t.Fatal("anonymous user must be NULL")
	}
	for _, bad := range []string{"raw-", "old-pass", "unchanged"} {
		if strings.Contains(diff, bad) {
			t.Fatalf("leak or unchanged field: %s", diff)
		}
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(diff), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["password"] != "已修改" || fields["nested.extra"] != "已修改" || fields["headers"] != "已修改" {
		t.Fatalf("unexpected diff: %s", diff)
	}
	if _, ok := fields["nested.name"]; !ok {
		t.Fatal("missing field diff")
	}
	stop := errors.New("rollback")
	err := s.WithTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO settings(key,value) VALUES('test','true')`); err != nil {
			return err
		}
		if err := Write(t.Context(), tx, r); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	var n int
	if err := s.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM audit_logs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit not rolled back: %d %v", n, err)
	}
	if err := s.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM settings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("data not rolled back: %d %v", n, err)
	}
}
func TestInvalidJSONAndCancelled(t *testing.T) {
	s := testStore(t)
	cycle := map[string]any{}
	cycle["self"] = cycle
	for _, v := range []any{math.NaN(), math.Inf(1), func() {}, make(chan int), cycle, json.RawMessage(`bad`)} {
		err := s.WithTx(t.Context(), func(tx *sql.Tx) error { return Write(t.Context(), tx, Record{After: map[string]any{"password": v}}) })
		if err == nil {
			t.Fatalf("accepted invalid JSON %T", v)
		}
	}
	if err := Write(t.Context(), nil, Record{}); err == nil {
		t.Fatal("accepted nil tx")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := s.WithTx(t.Context(), func(tx *sql.Tx) error { return Write(ctx, tx, Record{}) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("audit ignored cancellation: %v", err)
	}
}
