package history

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTransactionBatchLimit(t *testing.T) {
	db, w := setup(t)
	base := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 401; i++ {
		w.Enqueue(state(base.Add(time.Duration(i)*time.Millisecond), float64(i)))
	}
	// 第 201 行报错：第一批 200 行必须已经提交，第二批事务完整回滚。
	exec(t, db, `CREATE TRIGGER fail_second_batch BEFORE INSERT ON history_sample WHEN (SELECT count(*) FROM history_sample)>=200 BEGIN SELECT RAISE(ABORT,'second batch'); END`)
	if err := w.Flush(t.Context()); err == nil {
		t.Fatal("expected failure")
	}
	if count(t, db, "history_sample") != 200 || len(w.queue) != 201 {
		t.Fatal("transactions were not limited to 200 or failed rows lost")
	}
	exec(t, db, `DROP TRIGGER fail_second_batch`)
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_sample") != 401 {
		t.Fatal("retry failed")
	}
}

func waitCount(t *testing.T, w *Writer, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if count(t, w.db, "history_sample") == want {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("did not write %d rows in %s", want, timeout)
		}
	}
}

func TestRunThresholdAndTimer(t *testing.T) {
	_, w := setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 200; i++ {
		w.Enqueue(state(base.Add(time.Duration(i)*time.Millisecond), 1))
	}
	// 未到 1 秒，200 行应触发事务；SQL 是外部阻塞，使用实际有界等待。
	waitCount(t, w, 200, 900*time.Millisecond)
	w.Enqueue(state(base.Add(time.Second), 2))
	waitCount(t, w, 201, 2*time.Second)
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunCancellationKeepsPendingAndCloseIsIndependent(t *testing.T) {
	_, w := setup(t)
	w.Enqueue(state(time.Now(), 12))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run cancellation: %v", err)
	}
	if len(w.queue) != 1 || w.Enqueue(state(time.Now(), 1)) {
		t.Fatal("cancel did not retain/stop queue")
	}
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count(t, w.db, "history_sample") != 1 {
		t.Fatal("close after cancellation lost pending sample")
	}
}

func TestConcurrentFlushClose(t *testing.T) {
	_, w := setup(t)
	base := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 600; i++ {
		w.Enqueue(state(base.Add(time.Duration(i)*time.Millisecond), 1))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 3)
	go func() { done <- w.Flush(ctx) }()
	go func() { done <- w.Close(ctx) }()
	go func() { done <- w.Close(ctx) }()
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if count(t, w.db, "history_sample") != 600 {
		t.Fatal("concurrent flush lost data")
	}
}

func TestRetentionEdgeDoesNotOverwriteCompleteAggregate(t *testing.T) {
	db, w := setup(t)
	now := time.Now().UTC().Truncate(time.Minute)
	base := now.Add(-7 * 24 * time.Hour)
	w.Enqueue(state(base.Add(time.Second), 10))
	w.Enqueue(state(base.Add(40*time.Second), 30))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(30 * time.Second)
	if err := w.Cleanup(t.Context(), later); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "history_sample") != 1 {
		t.Fatal("expected partial retained minute")
	}
	// 强制把残缺分钟标脏；即使有迟到覆盖，也不能越过保留期完整分钟下界。
	w.Enqueue(state(base.Add(40*time.Second), 100))
	if err := w.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Aggregate(t.Context(), later); err != nil {
		t.Fatal(err)
	}
	result, err := w.Query(t.Context(), Query{DeviceID: 1, From: base, To: base.Add(time.Minute), Resolution: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 1 || result.Points[0].Fields["battery_voltage"].Count != 2 || result.Points[0].Fields["battery_voltage"].Avg != 20 {
		t.Fatal("partial detail overwrote complete aggregate", result)
	}
}
