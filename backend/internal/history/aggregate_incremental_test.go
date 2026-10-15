package history

import (
	"context"
	"errors"
	"testing"
	"time"
)

func aggregateValue(t *testing.T, w *Writer, minute time.Time) AggregateValue {
	t.Helper()
	result, err := w.Query(t.Context(), Query{DeviceID: 1, From: minute, To: minute.Add(time.Minute), Resolution: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 1 {
		t.Fatalf("minute %s: expected one point, got %+v", minute, result)
	}
	return result.Points[0].Fields["battery_voltage"]
}

func TestAggregateSkipsUnchangedMinutesAndRestartBackfills(t *testing.T) {
	db, w := setup(t)
	now := time.Now().UTC().Truncate(time.Minute)
	old := now.Add(-3 * time.Minute)
	w.Enqueue(state(old.Add(time.Second), 10))
	w.Enqueue(state(now.Add(time.Second), 20))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_1m") != 1 {
		t.Fatal("incomplete minute aggregated")
	}
	// 合法 JSON、非法字段值：任何再次解码旧分钟的实现都会失败。
	exec(t, db, `UPDATE history_sample SET data='{"battery_voltage":null}' WHERE ts=?`, old.Add(time.Second).UnixMilli())
	if err := w.Aggregate(t.Context(), now.Add(40*time.Second)); err != nil {
		t.Fatalf("unchanged minute was reread: %v", err)
	}
	if err := w.Aggregate(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatalf("advancing watermark reread old minute: %v", err)
	}
	if value := aggregateValue(t, w, now); value.Count != 1 || value.Avg != 20 {
		t.Fatalf("incomplete minute was lost on advancement: %+v", value)
	}
	// 新 Writer 无内存水位，必须重读旧分钟，而不能只跟随已有聚合末尾。
	restarted, err := New(db, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Aggregate(t.Context(), now.Add(time.Minute)); err == nil {
		t.Fatal("restart did not backfill retained detail")
	}
	exec(t, db, `UPDATE history_sample SET data='{"battery_voltage":30}' WHERE ts=?`, old.Add(time.Second).UnixMilli())
	if err := restarted.Aggregate(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if value := aggregateValue(t, restarted, old); value.Count != 1 || value.Avg != 30 {
		t.Fatalf("restart did not replace old aggregate: %+v", value)
	}
}

func TestAggregateLateAndOverwriteUseEarliestDirtyMinute(t *testing.T) {
	db, w := setup(t)
	now := time.Now().UTC().Truncate(time.Minute)
	old := now.Add(-3 * time.Minute)
	late := old.Add(time.Minute)
	w.Enqueue(state(old.Add(time.Second), 1))
	w.Enqueue(state(late.Add(time.Second), 10))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `UPDATE history_sample SET data='{"battery_voltage":null}' WHERE ts=?`, old.Add(time.Second).UnixMilli())
	// 同批包含迟到和未完成分钟；清掉最早 dirty 后，后者仍由水位接续。
	w.Enqueue(state(now.Add(time.Second), 50))
	w.Enqueue(state(late.Add(2*time.Second), 30))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now.Add(30*time.Second)); err != nil {
		t.Fatalf("dirty range included unaffected earlier minute: %v", err)
	}
	if value := aggregateValue(t, w, late); value.Count != 2 || value.Avg != 20 {
		t.Fatalf("late sample was not aggregated: %+v", value)
	}
	w.Enqueue(state(late.Add(time.Second), 40))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	if value := aggregateValue(t, w, late); value.Count != 2 || value.Min != 30 || value.Max != 40 || value.Avg != 35 {
		t.Fatalf("same timestamp overwrite was not aggregated: %+v", value)
	}
	if err := w.Aggregate(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if value := aggregateValue(t, w, now); value.Count != 1 || value.Avg != 50 {
		t.Fatalf("mixed batch lost its incomplete minute: %+v", value)
	}
}

func TestAggregateFailureRetainsProgressForRetry(t *testing.T) {
	for _, startup := range []bool{true, false} {
		name := "incremental"
		if startup {
			name = "startup"
		}
		t.Run(name, func(t *testing.T) {
			db, w := setup(t)
			now := time.Now().UTC().Truncate(time.Minute)
			first := now.Add(-3 * time.Minute)
			second := first.Add(time.Minute)
			if !startup {
				if err := w.Aggregate(t.Context(), now.Add(-time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			w.Enqueue(state(first.Add(time.Second), 10))
			w.Enqueue(state(second.Add(time.Second), 20))
			if err := w.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			ready, until, dirtyFrom := w.aggregateReady, w.aggregateUntil, w.dirtyFrom
			exec(t, db, `CREATE TRIGGER reject_aggregate BEFORE INSERT ON history_1m WHEN (SELECT count(*) FROM history_1m)>=1 BEGIN SELECT RAISE(ABORT,'aggregate failure'); END`)
			if err := w.Aggregate(t.Context(), now); err == nil {
				t.Fatal("expected aggregate failure")
			}
			if count(t, db, "history_1m") != 1 {
				t.Fatal("expected first minute committed before second minute failed")
			}
			if w.aggregateReady != ready || w.aggregateUntil != until || !w.dirty || w.dirtyFrom != dirtyFrom {
				t.Fatal("failed aggregate changed progress or cleared dirty minute")
			}
			exec(t, db, `DROP TRIGGER reject_aggregate`)
			if err := w.Aggregate(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			if !w.aggregateReady || w.aggregateUntil != now.UnixMilli() || w.dirty || count(t, db, "history_1m") != 2 {
				t.Fatal("retry did not finish and advance progress")
			}
			if value := aggregateValue(t, w, first); value.Count != 1 || value.Avg != 10 {
				t.Fatalf("retry duplicated committed minute: %+v", value)
			}
			if value := aggregateValue(t, w, second); value.Count != 1 || value.Avg != 20 {
				t.Fatalf("retry missed failed minute: %+v", value)
			}
		})
	}
}

func TestFailedFlushDoesNotMarkDirtyUntilRetryCommits(t *testing.T) {
	db, w := setup(t)
	now := time.Now().UTC().Truncate(time.Minute)
	old := now.Add(-time.Minute)
	w.Enqueue(state(old.Add(time.Second), 10))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	w.Enqueue(state(old.Add(2*time.Second), 30))
	exec(t, db, `CREATE TRIGGER reject_detail BEFORE INSERT ON history_sample BEGIN SELECT RAISE(ABORT,'detail failure'); END`)
	if err := w.Flush(t.Context()); err == nil {
		t.Fatal("expected flush failure")
	}
	if w.dirty || len(w.queue) != 1 {
		t.Fatal("failed flush marked dirty or lost queued sample")
	}
	exec(t, db, `DROP TRIGGER reject_detail`)
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !w.dirty || w.dirtyFrom != old.UnixMilli() {
		t.Fatal("successful flush retry did not mark its minute dirty")
	}
	if err := w.Aggregate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if value := aggregateValue(t, w, old); value.Count != 2 || value.Avg != 20 {
		t.Fatalf("flush retry did not reaggregate late sample: %+v", value)
	}
}

func TestAggregateConcurrentFlushRetainsLateSample(t *testing.T) {
	db, w := setup(t)
	now := time.Now().UTC().Truncate(time.Minute)
	old := now.Add(-2 * time.Minute)
	w.Enqueue(state(old.Add(time.Second), 10))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	w.Enqueue(state(now.Add(-time.Minute+time.Second), 50))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// 占用唯一写连接，使 Aggregate 在读取分钟列表后、提交聚合前暂停。
	conn, err := db.Write().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waits := db.Write().Stats().WaitCount
	done := make(chan error, 2)
	go func() { done <- w.Aggregate(ctx, now) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for db.Write().Stats().WaitCount == waits {
		select {
		case err := <-done:
			t.Fatalf("aggregate did not wait for write connection: %v", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if len(w.flushGate) != 1 {
		t.Fatal("Aggregate does not hold the Flush gate while processing")
	}
	w.Enqueue(state(old.Add(2*time.Second), 30))
	go func() { done <- w.Flush(ctx) }()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := w.Aggregate(ctx, now); err != nil {
		t.Fatal(err)
	}
	if value := aggregateValue(t, w, old); value.Count != 2 || value.Avg != 20 {
		t.Fatalf("concurrent Flush lost the late dirty mark: %+v", value)
	}
	if value := aggregateValue(t, w, now.Add(-time.Minute)); value.Count != 1 || value.Avg != 50 {
		t.Fatalf("concurrent aggregation lost advancing minute: %+v", value)
	}
}

func TestAggregateGateWaitCanBeCancelled(t *testing.T) {
	_, w := setup(t)
	w.flushGate <- struct{}{}
	defer func() { <-w.flushGate }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Aggregate(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected gate cancellation, got %v", err)
	}
	if w.aggregateReady || w.dirty {
		t.Fatal("cancelled aggregate changed progress")
	}
}
