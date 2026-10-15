package api

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRequiresBusinessModules(t *testing.T) {
	f := newAPI(t)
	for _, tc := range []struct {
		name   string
		remove func(*Options)
	}{
		{"db", func(o *Options) { o.DB = nil }},
		{"cipher", func(o *Options) { o.Cipher = nil }},
		{"auth", func(o *Options) { o.Auth = nil }},
		{"collector", func(o *Options) { o.Collector = nil }},
		{"history", func(o *Options) { o.History = nil }},
		{"bus", func(o *Options) { o.Bus = nil }},
		{"runtime", func(o *Options) { o.Runtime = nil }},
		{"notify", func(o *Options) { o.Notify = nil }},
		{"actions", func(o *Options) { o.Actions = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := f.s.opts
			tc.remove(&opts)
			if server, err := New(opts); err == nil || server != nil {
				t.Fatal("缺少必需模块仍构造成功")
			}
		})
	}
}

func TestAuditEncodingFailureRollsBackChange(t *testing.T) {
	f := newAPI(t)
	r := httptest.NewRequest("POST", "/api/v1/devices", nil).WithContext(t.Context())
	for _, bad := range []any{map[string]any{"number": math.NaN()}, []string{"not an object"}} {
		for _, before := range []bool{false, true} {
			err := f.s.change(r, nil, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(t.Context(), "INSERT INTO settings(key,value) VALUES('rollback','{}')"); err != nil {
					return err
				}
				if before {
					return logChange(t.Context(), tx, r, "test", "test", bad, nil)
				}
				return logChange(t.Context(), tx, r, "test", "test", nil, bad)
			})
			if err == nil {
				t.Fatal("审计编码失败被伪装为成功")
			}
			var count int
			if err := f.db.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM settings WHERE key='rollback'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("审计失败仍提交了配置")
			}
		}
	}
}

type codedFailure struct{}

func (codedFailure) Error() string { return "private-password" }
func (codedFailure) Code() int     { return 5 }

func TestInternalFailureLogsSafeDiagnostics(t *testing.T) {
	f := newAPI(t)
	var logs bytes.Buffer
	f.s.opts.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	w := httptest.NewRecorder()
	f.s.internal(w, fmt.Errorf("private-password: %w", codedFailure{}))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	if !strings.Contains(logs.String(), "sqlite_5") || !strings.Contains(logs.String(), "dependencies_test.go") || !strings.Contains(logs.String(), "line=") {
		t.Fatal("诊断缺少分类或位置", logs.String())
	}
	if strings.Contains(logs.String()+w.Body.String(), "private-password") {
		t.Fatal("诊断泄露错误原文")
	}
}
