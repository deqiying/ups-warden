package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

func setup(t *testing.T) (*store.Store, *Writer) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Write().ExecContext(t.Context(), `INSERT INTO devices(id,name) VALUES(1,'test'),(2,'other')`); err != nil {
		t.Fatal(err)
	}
	w, err := New(db, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return db, w
}

func state(at time.Time, value float64) model.DeviceState {
	return model.DeviceState{DeviceID: 1, At: at, Fields: map[string]model.Field{"battery_voltage": {Num: value, Quality: model.Good}}}
}

func exec(t *testing.T, db *store.Store, query string, args ...any) {
	t.Helper()
	if _, err := db.Write().ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *store.Store, table string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFilterCopyAndNoProtocolData(t *testing.T) {
	db, w := setup(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	s := state(at, 12)
	s.Fields["output_voltage[1]"] = model.Field{Num: 230, Quality: model.Good, Reason: "OID-secret"}
	s.Fields["output_voltage"] = model.Field{Num: 999, Quality: model.Good}
	s.Fields["input_voltage[4]"] = model.Field{Num: 999, Quality: model.Good}
	s.Fields["rated_va"] = model.Field{Num: 1000, Quality: model.Good}
	s.Fields["temperature"] = model.Field{Num: math.NaN(), Quality: model.Good}
	s.Fields["battery_current"] = model.Field{Num: math.Inf(1), Quality: model.Good}
	s.Fields["battery_capacity_pct"] = model.Field{Num: 42, Quality: model.Stale}
	s.Fields["native_runtime_remaining"] = model.Field{Num: 20, Quality: model.Good}
	s.Fields["mains_ok"] = model.Field{Bool: true, Quality: model.Good}
	s.Fields["power_state"] = model.Field{Str: "on_battery", Quality: model.Good}
	s.Fields["raw_trace"] = model.Field{Str: "QAD RTD source host password", Quality: model.Good}
	if !w.Enqueue(s) {
		t.Fatal("enqueue")
	}
	s.Fields["battery_voltage"] = model.Field{Num: 99, Quality: model.Good}
	clear(s.Fields)
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var data string
	if err := db.Read().QueryRowContext(t.Context(), `SELECT data FROM history_sample`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var fields map[string]float64
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["battery_voltage"] != 12 || fields["output_voltage[1]"] != 230 || fields["native_runtime_remaining"] != 20 {
		t.Fatalf("unexpected data: %s", data)
	}
	if strings.Contains(data, "source") || strings.Contains(data, "OID") || strings.Contains(data, "raw") || strings.Contains(data, "quality") {
		t.Fatalf("protocol metadata: %s", data)
	}
	if err := w.Aggregate(t.Context(), at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.Read().QueryRowContext(t.Context(), `SELECT data FROM history_1m`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var aggregated map[string]AggregateValue
	if err := json.Unmarshal([]byte(data), &aggregated); err != nil {
		t.Fatal(err)
	}
	if len(aggregated) != 3 {
		t.Fatalf("aggregate leaked fields: %s", data)
	}
	if w.Enqueue(model.DeviceState{DeviceID: 1, At: at, Fields: map[string]model.Field{"raw": {Quality: model.Good}}}) {
		t.Fatal("arbitrary field bypass")
	}
}

func TestBatchOverflowRetryAndClose(t *testing.T) {
	db, w := setup(t)
	at := time.Now().Truncate(time.Millisecond)
	exec(t, db, `CREATE TRIGGER reject_history BEFORE INSERT ON history_sample BEGIN SELECT RAISE(ABORT,'failure'); END`)
	for i := 0; i < queueLimit; i++ {
		if !w.Enqueue(state(at.Add(time.Duration(i)*time.Millisecond), float64(i))) {
			t.Fatal("early overflow", i)
		}
	}
	if w.Enqueue(state(at, 1)) || w.Dropped() != 1 {
		t.Fatal("overflow not counted")
	}
	if err := w.Flush(t.Context()); err == nil {
		t.Fatal("expected transaction failure")
	}
	if len(w.queue) != queueLimit || count(t, db, "history_sample") != 0 {
		t.Fatal("failed batch lost/partially committed")
	}
	exec(t, db, `DROP TRIGGER reject_history`)
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_sample") != queueLimit || len(w.queue) != 0 {
		t.Fatal("not flushed")
	}
	if w.Enqueue(state(at, 1)) {
		t.Fatal("enqueue after close")
	}
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	// 重复时间戳按主键替换，不能重复计数。
	other, err := New(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	other.Enqueue(state(at, 42))
	if err := other.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_sample") != queueLimit {
		t.Fatal("duplicate timestamp")
	}
}

func TestCancelledFlushAndCloseWithoutRun(t *testing.T) {
	_, w := setup(t)
	w.Enqueue(state(time.Now(), 12))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close error %v", err)
	}
	if len(w.queue) != 1 {
		t.Fatal("cancelled close lost data")
	}
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(w.queue) != 0 {
		t.Fatal("retry close")
	}
}

func TestRunCloseAndOneShot(t *testing.T) {
	_, w := setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	w.Enqueue(state(time.Now(), 12))
	closeCtx, closeCancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer closeCancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-closeCtx.Done():
		t.Fatal("Close deadlocked before run cancellation")
	}
	if err := w.Run(t.Context()); err == nil {
		t.Fatal("second Run accepted")
	}
}

func TestAggregateRestartLateAndCompleteMinute(t *testing.T) {
	db, w := setup(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	for i, v := range []float64{10, 20} {
		w.Enqueue(state(base.Add(time.Duration(i+1)*time.Second), v))
	}
	w.Enqueue(state(base.Add(3*time.Minute+time.Second), 99))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := base.Add(3*time.Minute + 30*time.Second)
	if err := w.Aggregate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_1m") != 1 {
		t.Fatal("incomplete minute included")
	}
	// 新 writer 不携带水位，仍能补算/重算已有分钟。
	restarted, err := New(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	restarted.Enqueue(state(base.Add(3*time.Second), 30))
	restarted.Enqueue(state(base.Add(time.Minute+time.Second), 40))
	if err := restarted.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := restarted.Aggregate(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	result, err := w.Query(t.Context(), Query{DeviceID: 1, From: base, To: now, Resolution: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 2 {
		t.Fatalf("points %v", result)
	}
	value := result.Points[0].Fields["battery_voltage"]
	if value.Count != 3 || value.Min != 10 || value.Max != 30 || math.Abs(value.Avg-20) > 1e-9 {
		t.Fatalf("late/idempotent %+v", value)
	}
	// 1 点降采样必须按 3:1 加权，不能得出 30。
	result, err = w.Query(t.Context(), Query{DeviceID: 1, From: base, To: now, Resolution: "1m", MaxPoints: 1})
	if err != nil {
		t.Fatal(err)
	}
	value = result.Points[0].Fields["battery_voltage"]
	if value.Count != 4 || math.Abs(value.Avg-25) > 1e-9 {
		t.Fatalf("unweighted %+v", value)
	}
}

func TestQueryBoundsAutoAndLimits(t *testing.T) {
	_, w := setup(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	for i := 0; i < 1001; i++ {
		if !w.Enqueue(state(base.Add(time.Duration(i)*time.Millisecond), float64(i))) {
			t.Fatal(i)
		}
	}
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	q := Query{DeviceID: 1, From: base, To: base.Add(time.Second), Resolution: "auto"}
	result, err := w.Query(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, p := range result.Points {
		total += p.Fields["battery_voltage"].Count
	}
	if result.Resolution != "raw" || total != 1000 || len(result.Points) > 1000 {
		t.Fatalf("bounds/limit %+v total %d", result, total)
	}
	q.From = base.Add(100*time.Millisecond + time.Nanosecond)
	q.To = base.Add(102 * time.Millisecond)
	result, err = w.Query(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 1 || result.Points[0].Fields["battery_voltage"].Avg != 101 {
		t.Fatal("submillisecond bounds", result)
	}
	q.From = base
	q.To = base.Add(1001 * time.Millisecond)
	q.MaxPoints = 1
	result, err = w.Query(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	value := result.Points[0].Fields["battery_voltage"]
	if len(result.Points) != 1 || value.Count != 1001 || math.Abs(value.Avg-500) > 1e-8 {
		t.Fatalf("raw downsample %+v", value)
	}
	q.To = base.Add(7 * time.Hour)
	result, err = w.Query(t.Context(), q)
	if err != nil || result.Resolution != "1m" {
		t.Fatal("long auto", result, err)
	}
	q.From = time.Now().Add(-8 * 24 * time.Hour)
	q.To = q.From.Add(time.Hour)
	result, err = w.Query(t.Context(), q)
	if err != nil || result.Resolution != "1m" {
		t.Fatal("expired auto", result, err)
	}
	for _, bad := range []Query{{DeviceID: 0, From: base, To: base.Add(time.Hour)}, {DeviceID: 1, From: base, To: base}, {DeviceID: 1, From: base, To: base.Add(time.Hour), Resolution: "DROP TABLE"}, {DeviceID: 1, From: base, To: base.Add(time.Hour), MaxPoints: -1}} {
		if _, err := w.Query(t.Context(), bad); err == nil {
			t.Fatalf("accepted invalid %+v", bad)
		}
	}
}

func TestCleanupBatchesAndActiveReferences(t *testing.T) {
	db, w := setup(t)
	now := time.Now().UTC().Truncate(time.Minute)
	expired := now.Add(-8 * 24 * time.Hour).UnixMilli()
	err := db.WithTx(t.Context(), func(tx *sql.Tx) error {
		for i := 0; i < 5003; i++ {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO history_sample VALUES(1,?, '{}')`, expired+int64(i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO history_sample VALUES(1,?, '{}')`, now.Add(-7*24*time.Hour).UnixMilli())
	exec(t, db, `INSERT INTO history_1m VALUES(1,?, '{}'),(2,?,'{}')`, now.Add(-91*24*time.Hour).UnixMilli(), now.Add(-90*24*time.Hour).UnixMilli())
	old := now.Add(-366 * 24 * time.Hour).UnixMilli()
	exec(t, db, `INSERT INTO events(id,device_id,type,severity,started_at,cleared_at,active) VALUES(1,1,'test','info',?,NULL,1),(2,1,'test','info',?,?,0),(3,1,'test','info',?,?,0)`, old, old, old, old, old)
	exec(t, db, `INSERT INTO channels(id,name,type,config) VALUES(1,'test','test','{}')`)
	exec(t, db, `INSERT INTO notify_logs(channel_id,event_id,at,summary,ok,duration_ms) VALUES(1,3,?,'',1,0)`, now.UnixMilli())
	exec(t, db, `INSERT INTO outage_cycles(id,device_id,started_at,ended_at) VALUES(1,1,?,NULL),(2,1,?,?),(3,1,?,?)`, old, old, old, old, old)
	exec(t, db, `INSERT INTO device_state(device_id,power_state,power_state_raw,outage_cycle_id,comm_state,updated_at) VALUES(1,'unknown','unknown',3,'lost',?)`, old)
	exec(t, db, `INSERT INTO audit_logs(at,kind,target,diff,result) VALUES(?,'test','','{}','ok'),(?,'test','','{}','ok')`, old, now.Add(-365*24*time.Hour).UnixMilli())
	if err := w.Cleanup(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_sample") != 1 || count(t, db, "history_1m") != 1 || count(t, db, "events") != 2 || count(t, db, "outage_cycles") != 2 || count(t, db, "audit_logs") != 1 {
		t.Fatal("cleanup retention/active reference mismatch")
	}
	if err := w.Cleanup(t.Context(), now); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsAndSynctestPureBoundaries(t *testing.T) {
	_, w := setup(t)
	if w.opts.DetailRetention != 7*24*time.Hour || w.opts.AggregateRetention != 90*24*time.Hour || w.opts.LogRetention != 365*24*time.Hour {
		t.Fatal("defaults")
	}
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("nil store")
	}
	if _, err := New(w.db, Options{DetailRetention: -1}); err == nil {
		t.Fatal("negative retention")
	}
	// SQL 属于外部阻塞，synctest 只验证纯时间边界和无数据库队列关闭。
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		time.Sleep(time.Second)
		if time.Since(now) != time.Second {
			t.Fatal("virtual timer")
		}
		isolated := &Writer{queue: make([]sample, 0, queueLimit), stop: make(chan struct{}), wake: make(chan struct{}, 1)}
		if !isolated.Enqueue(state(time.Now(), 1)) {
			t.Fatal("enqueue")
		}
		isolated.stopAccepting()
		if isolated.Enqueue(state(time.Now(), 1)) {
			t.Fatal("closed accepted")
		}
		if ceilMillis(time.UnixMilli(123).Add(time.Nanosecond)) != 124 {
			t.Fatal("ceil")
		}
	})
}
