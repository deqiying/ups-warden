package rule

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

type recorder struct {
	mu       sync.Mutex
	requests []action.Request
	fail     bool
}

func (r *recorder) Submit(_ context.Context, request action.Request) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return 0, errors.New("fixture-dispatch-failure")
	}
	r.requests = append(r.requests, request)
	return int64(len(r.requests)), nil
}
func (r *recorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.requests) }
func ruleFixture(t *testing.T, r Record) (*store.Store, *Service, *recorder) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, q := range []string{
		`INSERT INTO devices(id,name,tags) VALUES(1,'fixture','["lab"]'),(2,'other','[]')`,
		`INSERT INTO actions(id,name,type,config,timeout_s) VALUES(1,'fixture','script','{}',30),(2,'recovery','script','{}',30)`,
	} {
		if _, err := db.Write().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	scope, trigger, actions, recovery, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Write().Exec(`INSERT INTO rules(id,name,enabled,dry_run,scope,trigger,actions,recovery_actions,cooldown_s) VALUES(1,?,?,?,?,?,?,?,?)`, r.Name, r.Enabled, r.DryRun, scope, trigger, actions, recovery, r.CooldownS); err != nil {
		t.Fatal(err)
	}
	recorder := &recorder{}
	s, err := New(t.Context(), db, recorder, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, s, recorder
}
func TestWriteAheadRollbackRestartAndRecovery(t *testing.T) {
	r := conditionRule(Condition{Field: "battery_capacity_pct", Op: "lte", Value: json.RawMessage(`40`)})
	db, s, sink := ruleFixture(t, r)
	u := update(time.Now(), "battery_capacity_pct", model.Field{Num: 30, Quality: model.Good})
	if _, err := db.Write().Exec(`CREATE TRIGGER reject_rule BEFORE INSERT ON rule_logs BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Process(t.Context(), u); err == nil || sink.count() != 0 {
		t.Fatal("日志失败却派发动作", err, sink.count())
	}
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM rule_state").Scan(&n); err != nil || n != 0 {
		t.Fatal("实例状态未回滚", n, err)
	}
	if _, err := db.Write().Exec("DROP TRIGGER reject_rule"); err != nil {
		t.Fatal(err)
	}
	if err := s.Process(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 || !sink.requests[0].DryRun || sink.requests[0].Trigger.RuleID != 1 || sink.requests[0].Event.Type != "rule_triggered" {
		t.Fatal(sink.requests)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), db, sink, s.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = restarted.Process(t.Context(), u); err != nil || sink.count() != 1 {
		t.Fatal("重启重复触发", err, sink.count())
	}
	u.State.Fields["battery_capacity_pct"] = model.Field{Num: 80, Quality: model.Good}
	if err = restarted.Process(t.Context(), u); err != nil || sink.count() != 2 {
		t.Fatal("恢复动作失败", err, sink.count())
	}
	if !sink.requests[1].Trigger.Recovery || sink.requests[1].ActionID != 2 {
		t.Fatal(sink.requests[1])
	}
	var ids string
	if err = db.Read().QueryRow("SELECT action_run_ids FROM rule_logs WHERE kind='fired'").Scan(&ids); err != nil || ids != "[1]" {
		t.Fatal(ids, err)
	}
}
func TestUnfinishedDurationRestartDoesNotCountDowntime(t *testing.T) {
	r := conditionRule(Condition{Field: "temperature", Op: "gt", Value: json.RawMessage(`30`), ForS: 10})
	db, s, sink := ruleFixture(t, r)
	at := time.Now()
	u := update(at, "temperature", model.Field{Num: 40, Quality: model.Good})
	if err := s.Process(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(t.Context(), db, sink, s.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	u.State.At = at.Add(time.Hour)
	if err = next.Process(t.Context(), u); err != nil || sink.count() != 0 {
		t.Fatal("未确认窗口跨停机延续", err)
	}
	u.State.At = u.State.At.Add(10 * time.Second)
	if err = next.Process(t.Context(), u); err != nil || sink.count() != 1 {
		t.Fatal(err, sink.count())
	}
}
func TestScopesDispatchFailureAndDisabledDevice(t *testing.T) {
	r := conditionRule(Condition{Field: "low_battery", Op: "eq", Value: json.RawMessage(`true`)})
	r.Scope = Scope{Type: "tags", Tags: []string{"lab"}}
	db, s, sink := ruleFixture(t, r)
	u := update(time.Now(), "low_battery", model.Field{Bool: true, Quality: model.Good})
	u.State.DeviceID = 2
	if err := s.Process(t.Context(), u); err != nil || sink.count() != 0 {
		t.Fatal(err)
	}
	u.State.DeviceID = 1
	sink.fail = true
	if err := s.Process(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := db.Read().QueryRow("SELECT kind FROM rule_logs ORDER BY id DESC LIMIT 1").Scan(&kind); err != nil || kind != "dispatch_failed" {
		t.Fatal(kind, err)
	}
	sink.fail = false
	if err := s.Process(t.Context(), u); err != nil || sink.count() != 0 {
		t.Fatal("派发失败自动重放", err)
	}
	if _, err := db.Write().Exec("UPDATE devices SET enabled=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	u.State.Fields["low_battery"] = model.Field{Quality: model.Good}
	if err := s.Process(t.Context(), u); err != nil || sink.count() != 0 {
		t.Fatal("禁用设备触发恢复", err)
	}
}
func TestQueueCopiesInputAndDrainsOnClose(t *testing.T) {
	r := conditionRule(Condition{Field: "low_battery", Op: "eq", Value: json.RawMessage(`true`)})
	_, s, sink := ruleFixture(t, r)
	u := update(time.Now(), "low_battery", model.Field{Bool: true, Quality: model.Good})
	if err := s.Enqueue(u); err != nil {
		t.Fatal(err)
	}
	u.State.Fields["low_battery"] = model.Field{Quality: model.Good}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatal("队列未排空或共享输入", sink.count())
	}
	if err := s.Enqueue(u); err == nil {
		t.Fatal("关闭后仍接受输入")
	}
}
