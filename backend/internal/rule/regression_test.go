package rule

import (
	"encoding/json"
	"testing"
	"time"

	"ups-warden/internal/model"
)

func TestCompletionFailureDoesNotLoseRemainingJobsOrReplay(t *testing.T) {
	r := conditionRule(Condition{Field: "low_battery", Op: "eq", Value: json.RawMessage(`true`)})
	db, s, sink := ruleFixture(t, r)
	if _, err := db.Write().Exec(`INSERT INTO rules(name,enabled,dry_run,scope,trigger,actions,recovery_actions,cooldown_s) SELECT 'second',enabled,dry_run,scope,trigger,actions,recovery_actions,cooldown_s FROM rules WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write().Exec(`CREATE TRIGGER reject_link BEFORE UPDATE ON rule_logs BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	u := update(time.Now(), "low_battery", model.Field{Bool: true, Quality: model.Good})
	if err := s.Process(t.Context(), u); err == nil {
		t.Fatal("补写失败被吞掉")
	}
	if sink.count() != 2 {
		t.Fatal("补写失败跳过后续已提交命中", sink.count())
	}
	if err := s.Process(t.Context(), u); err == nil || sink.count() != 2 {
		t.Fatal("补写重试重新执行动作", err, sink.count())
	}
	if _, err := db.Write().Exec("DROP TRIGGER reject_link"); err != nil {
		t.Fatal(err)
	}
	if err := s.Process(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 2 {
		t.Fatal("恢复补写后重放动作", sink.count())
	}
	var count int
	if err := db.Read().QueryRow(`SELECT count(*) FROM rule_logs WHERE action_run_ids!='[]'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("关联 ID 未恢复", count, err)
	}
}

func TestDevicePauseResetsOnlyUnconfirmedTimers(t *testing.T) {
	r := conditionRule(Condition{Field: "temperature", Op: "gt", Value: json.RawMessage(`30`), ForS: 60})
	_, s, sink := ruleFixture(t, r)
	at := time.Now()
	u := update(at, "temperature", model.Field{Num: 40, Quality: model.Good})
	if err := s.Enqueue(u); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetTimers(t.Context(), []int64{1}); err != nil {
		t.Fatal(err)
	} // 必须排在旧输入之后。
	u.State.At = at.Add(time.Hour)
	if err := s.Process(t.Context(), u); err != nil || sink.count() != 0 {
		t.Fatal("把停采时间算进窗口", err, sink.count())
	}
	u.State.At = u.State.At.Add(time.Minute)
	if err := s.Process(t.Context(), u); err != nil || sink.count() != 1 {
		t.Fatal(err, sink.count())
	}
	if err := s.ResetTimers(t.Context(), []int64{1}); err != nil {
		t.Fatal(err)
	}
	u.State.At = u.State.At.Add(time.Hour)
	if err := s.Process(t.Context(), u); err != nil || sink.count() != 1 {
		t.Fatal("暂停重置已消费边沿", err, sink.count())
	}
}

func TestBeginCloseUnblocksFullQueueBeforeProducerExit(t *testing.T) {
	r := conditionRule(Condition{Field: "low_battery", Op: "eq", Value: json.RawMessage(`true`)})
	_, s, sink := ruleFixture(t, r)
	u := update(time.Now(), "low_battery", model.Field{Bool: true, Quality: model.Good})
	s.processMu.Lock()
	for range cap(s.inbox) + 1 {
		if err := s.Enqueue(u); err != nil {
			s.processMu.Unlock()
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- s.Enqueue(u) }()
	s.BeginClose()
	select {
	case err := <-done:
		if err == nil {
			s.processMu.Unlock()
			t.Fatal("满队列停机没有拒绝等待输入")
		}
	case <-time.After(time.Second):
		s.processMu.Unlock()
		t.Fatal("满队列导致停机死锁")
	}
	s.processMu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatal("已接受输入未排空或重复执行", sink.count())
	}
}

func TestUntypedRecoveryAndReusedEventID(t *testing.T) {
	r := conditionRule(Condition{})
	r.Trigger = Trigger{Type: "event", Events: []string{model.EventUPSFault}}
	st := instance{}
	at := time.Now()
	first := model.DeviceUpdate{Events: []model.Event{{ID: 42, Type: model.EventUPSFault, At: at, Active: true}}}
	if got := evaluate(r, &st, first); len(got) != 1 || got[0].Kind != "fired" {
		t.Fatal(got)
	}
	clear := model.DeviceUpdate{ClearedEvents: []model.Event{{Clears: model.EventUPSFault, At: at.Add(time.Second)}}}
	if got := evaluate(r, &st, clear); len(got) != 1 || !got[0].Recovery {
		t.Fatal("无独立事件的恢复丢失", got)
	}
	if got := evaluate(r, &st, clear); len(got) != 0 {
		t.Fatal("重复恢复", got)
	}
	// 旧事件清理后 rowid 重新从 1 开始，新事实仍须被评估。
	next := model.DeviceUpdate{Events: []model.Event{{ID: 1, Type: model.EventUPSFault, At: at.Add(366 * 24 * time.Hour), Active: true}}}
	if got := evaluate(r, &st, next); len(got) != 1 || got[0].Kind != "fired" {
		t.Fatal("旧ID水位屏蔽新事件", got)
	}
	if got := evaluate(r, &st, next); len(got) != 0 {
		t.Fatal("重用ID后不再去重", got)
	}
}
