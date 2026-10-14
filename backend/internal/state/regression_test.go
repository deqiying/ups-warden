package state

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
	"ups-warden/internal/model"
)

func TestCompoundAlarmNeedsTriggerEvidence(t *testing.T) {
	for _, tc := range []struct {
		field, flag, raise, clear string
		trigger, recovery         float64
	}{{"battery_capacity_pct", "low_battery", model.EventBatteryLow, model.EventBatteryLowCleared, 20, 40}, {"output_load_pct[1]", "overload", model.EventOverload, model.EventOverloadCleared, 95, 50}} {
		t.Run(tc.field, func(t *testing.T) {
			db := stateDB(t)
			a := actor(t, db, DefaultConfig(), nil)
			at := time.Now()
			r := reading(at, "online", 90)
			r.Snapshot.Fields[tc.flag] = model.Field{Bool: false, Quality: model.Good}
			r.Snapshot.Fields[tc.field] = model.Field{Num: tc.trigger, Quality: model.Good}
			if !hasEvent(process(t, a, r), tc.raise) {
				t.Fatal("未触发")
			}
			// 原触发依据必须跨重启保存，不能让正常的另一依据替代恢复证明。
			a = actor(t, db, DefaultConfig(), nil)
			r.At = at.Add(time.Second)
			r.Snapshot.Fields[tc.field] = model.Field{Quality: model.Invalid}
			if hasEvent(process(t, a, r), tc.clear) {
				t.Fatal("invalid 被其他正常依据覆盖并清除告警")
			}
			r.At = at.Add(2 * time.Second)
			r.Snapshot.Fields[tc.field] = model.Field{Num: tc.recovery, Quality: model.Good}
			if !hasEvent(process(t, a, r), tc.clear) {
				t.Fatal("真实恢复未清除告警")
			}
		})
	}
}

func TestNativeRuntimeKeepsCapacityWindowBounded(t *testing.T) {
	e := mustEngine(t, DefaultConfig())
	at := time.Now()
	for i := 0; i <= 30; i++ {
		r := reading(at.Add(time.Duration(i)*time.Minute), "on_battery", 100-float64(i))
		r.Snapshot.Fields["native_runtime_remaining"] = model.Field{Num: 1000, Quality: model.Good}
		e.Apply(r)
	}
	if len(e.samples) > 11 {
		t.Fatalf("原生剩余时间导致窗口无界: %d", len(e.samples))
	}
	r := reading(at.Add(31*time.Minute), "on_battery", 69)
	u := e.Apply(r)
	if u.State.RuntimeSource != "estimated" || u.State.Fields["runtime_estimate"].Num != 4140 {
		t.Fatal("原生切换估算错误")
	}
}

func TestActorCancellationDrainsAcceptedFact(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := stateDB(t)
		a := actor(t, db, DefaultConfig(), nil)
		conn, err := db.Write().Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()
		r := reading(time.Now(), "online", 20)
		a.inbox <- input{result: &r, ack: make(chan error, 1)}
		synctest.Wait() // actor 已进入事务并等待唯一写连接。
		queued := reading(time.Now().Add(time.Second), "online", 19)
		a.inbox <- input{result: &queued, ack: make(chan error, 1)}
		cancel()
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal("正常取消被当作失败", err)
		}
		if a.Snapshot().Fields["battery_capacity_pct"].Num != 19 || count(t, db, "SELECT count(*) FROM events WHERE type='battery_low'") != 1 {
			t.Fatal("最后已接受事实丢失")
		}
	})
}
