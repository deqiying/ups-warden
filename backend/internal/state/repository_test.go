package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

func stateDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	execSQL(t, db, "INSERT INTO devices(id,name) VALUES(1,'test')")
	return db
}
func execSQL(t *testing.T, db *store.Store, q string) {
	t.Helper()
	if err := db.WithTx(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(q); return err }); err != nil {
		t.Fatal(err)
	}
}
func count(t *testing.T, db *store.Store, q string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func actor(t *testing.T, db *store.Store, c Config, p func(model.DeviceUpdate)) *Actor {
	t.Helper()
	a, err := NewActor(context.Background(), db, 1, c, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func process(t *testing.T, a *Actor, r model.SourceResult) model.DeviceUpdate {
	t.Helper()
	u, err := a.Process(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestTransactionRollbackAndRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := stateDB(t)
		c := DefaultConfig()
		published := 0
		a := actor(t, db, c, func(model.DeviceUpdate) { published++ })
		at := time.Now()
		process(t, a, reading(at, "online", 99))
		time.Sleep(time.Second)
		start := time.Now()
		process(t, a, reading(start, "on_battery", 99))
		before := published
		execSQL(t, db, `CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'test failure'); END`)
		time.Sleep(10 * time.Second)
		if _, err := a.Advance(context.Background(), time.Now()); err == nil {
			t.Fatal("未注入事务错误")
		}
		if published != before || a.Snapshot().PowerState != "online" || count(t, db, "SELECT count(*) FROM outage_cycles") != 0 {
			t.Fatal("事务失败污染状态/周期/发布")
		}
		execSQL(t, db, "DROP TRIGGER reject_event")
		u, err := a.Advance(context.Background(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !hasEvent(u, model.EventMainsLost) || u.State.OutageCycleID == nil {
			t.Fatal("重试未提交")
		}
		var started int64
		if err := db.Read().QueryRow("SELECT started_at FROM outage_cycles").Scan(&started); err != nil || started != start.UnixMilli() {
			t.Fatalf("周期起点 %d %v", started, err)
		}
		time.Sleep(time.Second)
		b := actor(t, db, c, nil)
		u = process(t, b, reading(time.Now(), "on_battery", 98))
		if hasEvent(u, model.EventMainsLost) || *u.State.OutageCycleID != *a.Snapshot().OutageCycleID {
			t.Fatal("重启重复周期")
		}
		time.Sleep(time.Second)
		process(t, b, reading(time.Now(), "online", 98))
		time.Sleep(30 * time.Second)
		b = actor(t, db, c, nil)
		// 恢复去抖在重启后重新计满，不继承已等待的 30 秒。
		process(t, b, reading(time.Now(), "online", 98))
		time.Sleep(59 * time.Second)
		u, err = b.Advance(context.Background(), time.Now())
		if err != nil || hasEvent(u, model.EventMainsRestored) {
			t.Fatal("重启提前恢复", err)
		}
		time.Sleep(time.Second)
		u, err = b.Advance(context.Background(), time.Now())
		if err != nil || !hasEvent(u, model.EventMainsRestored) || u.State.OutageCycleID != nil {
			t.Fatal("恢复未结束周期", err)
		}
		if count(t, db, "SELECT count(*) FROM events") != 2 || count(t, db, "SELECT count(*) FROM events WHERE active=1") != 0 || count(t, db, "SELECT count(*) FROM outage_cycles WHERE ended_at IS NULL") != 0 {
			t.Fatal("事件/周期未原子关闭")
		}
		b = actor(t, db, c, nil)
		time.Sleep(time.Second)
		if u = process(t, b, reading(time.Now(), "online", 98)); len(u.Events) != 0 {
			t.Fatal("市电启动误报")
		}
	})
}

func TestActorSubmitConfigureStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := stateDB(t)
		a := actor(t, db, DefaultConfig(), nil)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()
		if err := a.Submit(ctx, reading(time.Now(), "online", 90)); err != nil {
			t.Fatal(err)
		}
		c := DefaultConfig()
		c.OutageConfirmSeconds = 0
		if err := a.Configure(ctx, c); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if err := a.Submit(ctx, reading(time.Now(), "on_battery", 90)); err != nil {
			t.Fatal(err)
		}
		if a.Snapshot().PowerState != "on_battery" {
			t.Fatal("配置未应用")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := a.Submit(context.Background(), reading(time.Now(), "online", 90)); err == nil {
			t.Fatal("停止后仍接受输入")
		}
	})
}
func TestActorQueueCancellation(t *testing.T) {
	db := stateDB(t)
	a := actor(t, db, DefaultConfig(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Submit(ctx, reading(time.Now(), "online", 90)); err == nil {
		t.Fatal("未响应取消")
	}
}

func TestConfigStrict(t *testing.T) {
	for _, s := range []string{`null`, `[]`, `{"comm_failures":null}`, `{"comm_failures":1,"comm_failures":2}`, `{"unknown":1}`, `{} {}`, `{"comm_failures":0}`, `{"battery_low_pct":99,"battery_hysteresis_pct":5}`, `{"comm_lost_policy":"bad"}`, `{"outage_confirm_s":-1}`} {
		if _, err := ParseConfig([]byte(s)); err == nil {
			t.Errorf("接受非法配置 %s", s)
		}
	}
	c, err := ParseConfig([]byte(`{"outage_confirm_s":0}`))
	if err != nil || c.OutageConfirmSeconds != 0 || c.RecoveryConfirmSeconds != 60 {
		t.Fatal(c, err)
	}
}

func FuzzParseConfig(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"outage_confirm_s":0}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if c, err := ParseConfig(b); err == nil {
			if err = c.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
