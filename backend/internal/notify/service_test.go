package notify

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/bus"
	"ups-warden/internal/model"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

type fakeChannel struct {
	send func(context.Context, Message) error
}

func (c *fakeChannel) Send(ctx context.Context, m Message) error { return c.send(ctx, m) }
func (*fakeChannel) Close()                                      {}

func TestRetryScheduleAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var at []time.Duration
		c := &fakeChannel{send: func(context.Context, Message) error { at = append(at, time.Since(start)); return errors.New("failed") }}
		n, err := sendWithRetry(t.Context(), c, Message{})
		if err == nil || n != 4 {
			t.Fatalf("attempts=%d err=%v", n, err)
		}
		want := []time.Duration{0, 2 * time.Second, 6 * time.Second, 14 * time.Second}
		for i, v := range at {
			if v != want[i] {
				t.Fatalf("attempt %d at %v want %v", i, v, want[i])
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		c.send = func(context.Context, Message) error { cancel(); return errors.New("failed") }
		n, err = sendWithRetry(ctx, c, Message{})
		if n != 1 || err == nil {
			t.Fatal(n, err)
		}
	})
}
func TestRetryPanicAndSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := &fakeChannel{send: func(context.Context, Message) error {
			calls++
			if calls < 3 {
				panic("private-token")
			}
			return nil
		}}
		n, err := sendWithRetry(t.Context(), c, Message{})
		if n != 3 || err != nil {
			t.Fatal(n, err)
		}
		c.send = func(context.Context, Message) error { panic("private-token") }
		n, err = sendWithRetry(t.Context(), c, Message{})
		if n != 4 || err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatal(n, err)
		}
	})
}
func serviceFixture(t *testing.T) (*Service, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "notify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	cipher, err := secret.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	b := &bus.Bus{}
	t.Cleanup(b.Close)
	logs := &bytes.Buffer{}
	s, err := New(t.Context(), db, cipher, b, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, logs
}
func addChannel(t *testing.T, s *Service) int64 {
	t.Helper()
	c := ChannelRecord{Name: "test", Type: "webhook", Enabled: true, Credentials: Credentials{URL: "http://127.0.0.1:1/never-called"}}
	NormalizeConfig(c.Type, &c.Config)
	cfg, secret, err := EncodeChannel(c, s.cipher)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = s.Change(t.Context(), 0, func() (int64, error) {
		result, e := s.db.Write().ExecContext(t.Context(), "INSERT INTO channels(name,type,config,secret,enabled) VALUES(?,?,?,?,1)", c.Name, c.Type, cfg, secret)
		if e != nil {
			return 0, e
		}
		id, e = result.LastInsertId()
		return id, e
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func replaceDeliveries(t *testing.T, s *Service, id int64, deliveries ...Delivery) *worker {
	t.Helper()
	s.mu.Lock()
	old := s.workers[id]
	delete(s.workers, id)
	s.mu.Unlock()
	old.cancel()
	<-old.done
	if old.err != nil {
		t.Fatal(old.err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &worker{record: old.record, deliveries: deliveries, queue: make(chan job, 256), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.workers[id] = w
	s.mu.Unlock()
	go s.run(w)
	return w
}
func testRequest() Request {
	return Request{DeviceName: "测试 UPS", Event: model.Event{Type: "test", Severity: model.SeverityInfo}}
}
func countLogs(t *testing.T, s *Service, where string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM notify_logs "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestChannelIsolationQueueBoundAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := serviceFixture(t)
		slow := addChannel(t, s)
		fast := addChannel(t, s)
		entered := make(chan struct{})
		replaceDeliveries(t, s, slow, Delivery{Target: "slow", Channel: &fakeChannel{send: func(ctx context.Context, _ Message) error {
			close(entered)
			<-ctx.Done()
			return errors.New("cancelled")
		}}})
		replaceDeliveries(t, s, fast, Delivery{Target: "fast", Channel: &fakeChannel{send: func(context.Context, Message) error { return nil }}})
		if err := s.Enqueue(t.Context(), slow, testRequest()); err != nil {
			t.Fatal(err)
		}
		<-entered
		for i := 0; i < 256; i++ {
			if err := s.Enqueue(t.Context(), slow, testRequest()); err != nil {
				t.Fatal(i, err)
			}
		}
		if err := s.Enqueue(t.Context(), slow, testRequest()); !errors.Is(err, ErrQueueFull) {
			t.Fatal(err)
		}
		if err := s.Enqueue(t.Context(), fast, testRequest()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if n := countLogs(t, s, "WHERE channel_id=? AND ok=1", fast); n != 1 {
			t.Fatal(n)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if n := countLogs(t, s, "WHERE channel_id=? AND ok=0", slow); n != 257 {
			t.Fatal(n)
		}
		if err := s.Enqueue(t.Context(), fast, testRequest()); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	})
}
func TestFallbackAndPerTargetResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, logs := serviceFixture(t)
		id := addChannel(t, s)
		_, err := s.db.Write().ExecContext(t.Context(), "INSERT INTO notify_templates VALUES('test','json','{{secret-not-a-variable}}')")
		if err != nil {
			t.Fatal(err)
		}
		success, failed := 0, 0
		replaceDeliveries(t, s, id,
			Delivery{Target: "1", Channel: &fakeChannel{send: func(_ context.Context, m Message) error {
				success++
				if !strings.Contains(m.Body, "通知测试") {
					t.Error(m.Body)
				}
				return nil
			}}},
			Delivery{Target: "2", Channel: &fakeChannel{send: func(context.Context, Message) error { failed++; return errors.New("HTTP 503") }}})
		updates, _, cancel := s.bus.SubscribeWithDone()
		defer cancel()
		if err := s.Enqueue(t.Context(), id, testRequest()); err != nil {
			t.Fatal(err)
		}
		u := <-updates
		if u.NotifyFailed == nil || u.NotifyFailed.Attempts != 4 || u.NotifyFailed.Target != "2" || !u.NotifyFailed.TemplateFallback {
			t.Fatalf("update=%+v", u)
		}
		if success != 1 || failed != 4 {
			t.Fatal(success, failed)
		}
		if countLogs(t, s, "WHERE channel_id=? AND template_fallback=1", id) != 2 {
			t.Fatal("未逐目标记录")
		}
		if !strings.Contains(logs.String(), "使用默认模板") || strings.Contains(logs.String(), "secret-not-a-variable") {
			t.Fatal(logs.String())
		}
		var persisted int
		if err := s.db.Read().QueryRowContext(t.Context(), "SELECT attempts FROM notify_logs WHERE id=?", u.NotifyFailed.ID).Scan(&persisted); err != nil || persisted != 4 {
			t.Fatal(persisted, err)
		}
	})
}
func TestChangeRollbackAndCancelledRequestRestoreWorker(t *testing.T) {
	s, _ := serviceFixture(t)
	id := addChannel(t, s)
	sentinel := errors.New("rollback")
	err := s.Change(t.Context(), id, func() (int64, error) {
		return id, s.db.WithTx(t.Context(), func(tx *sql.Tx) error {
			if _, e := tx.Exec("UPDATE channels SET enabled=0 WHERE id=?", id); e != nil {
				return e
			}
			return sentinel
		})
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	c, err := ReadChannel(t.Context(), s.db, s.cipher, id)
	if err != nil || !c.Enabled {
		t.Fatal(c, err)
	}
	if s.workers[id] == nil {
		t.Fatal("失败后未恢复")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = s.Change(ctx, id, func() (int64, error) { t.Fatal("取消仍修改"); return 0, nil })
	if !errors.Is(err, context.Canceled) || s.workers[id] == nil {
		t.Fatal(err)
	}
}
func TestStorageFailureDoesNotPublishSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := serviceFixture(t)
		id := addChannel(t, s)
		w := replaceDeliveries(t, s, id, Delivery{Target: "x", Channel: &fakeChannel{send: func(context.Context, Message) error { return nil }}})
		if _, err := s.db.Write().Exec("CREATE TRIGGER fail_notify BEFORE INSERT ON notify_logs BEGIN SELECT RAISE(ABORT,'private-db-value'); END"); err != nil {
			t.Fatal(err)
		}
		if err := s.Enqueue(t.Context(), id, testRequest()); err != nil {
			t.Fatal(err)
		}
		<-w.done
		if w.err == nil {
			t.Fatal("吞掉持久化错误")
		}
		if err := s.Enqueue(t.Context(), id, testRequest()); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if err := s.Close(); err == nil {
			t.Fatal("关闭伪装成功")
		}
		// 夹具仍需再次 Close，断言过原始错误后只消除测试清理的重复报告。
		s.closeErr = nil
	})
}
func TestStoreAndSummarySecrets(t *testing.T) {
	s, _ := serviceFixture(t)
	cred := Credentials{Token: "123:private-token", SigningKey: "private-signing", Headers: map[string]string{"Authorization": "private-header"}}
	summary := SafeSummary(strings.Repeat("中", 210)+"123:private-token", cred)
	if len([]rune(summary)) != 200 {
		t.Fatal(len([]rune(summary)))
	}
	summary = SafeSummary("123:private-token private-signing private-header", cred)
	for _, v := range []string{cred.Token, cred.SigningKey, cred.Headers["Authorization"]} {
		if strings.Contains(summary, v) {
			t.Fatal(summary)
		}
	}
	id := addChannel(t, s)
	c, err := ReadChannel(t.Context(), s.db, s.cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	var cfg string
	var encrypted []byte
	if err := s.db.Read().QueryRow("SELECT config,secret FROM channels WHERE id=?", id).Scan(&cfg, &encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, c.Credentials.URL) || bytes.Contains(encrypted, []byte(c.Credentials.URL)) {
		t.Fatal("明文保存凭据")
	}
	if _, err := New(t.Context(), nil, s.cipher, s.bus, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("接受缺失模块")
	}
}
