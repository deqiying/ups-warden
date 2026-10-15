package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/config"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
)

func TestNotificationLifecycleRestartAndCancellation(t *testing.T) {
	var calls atomic.Int32
	block := atomic.Bool{}
	entered := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		calls.Add(1)
		if block.Load() {
			close(entered)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	cfg := config.Config{DataDir: t.TempDir()}
	a, err := open(context.Background(), cfg, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if a != nil {
			if err := a.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	record := notify.ChannelRecord{Name: "loopback", Type: "webhook", Enabled: true, Credentials: notify.Credentials{URL: target.URL}}
	notify.NormalizeConfig(record.Type, &record.Config)
	encoded, secret, err := notify.EncodeChannel(record, a.cipher)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = a.notify.Change(t.Context(), 0, func() (int64, error) {
		res, e := a.db.Write().Exec("INSERT INTO channels(name,type,config,secret,enabled) VALUES(?,?,?,?,1)", record.Name, record.Type, encoded, secret)
		if e != nil {
			return 0, e
		}
		id, e = res.LastInsertId()
		return id, e
	})
	if err != nil {
		t.Fatal(err)
	}
	request := notify.Request{DeviceName: "fixture", Event: model.Event{Type: "test", Severity: model.SeverityInfo}}
	if err := a.notify.Enqueue(t.Context(), id, request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var n int
		if err := a.db.Read().QueryRowContext(ctx, "SELECT count(*) FROM notify_logs WHERE ok=1").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("通知未落库")
		case <-tick.C:
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = open(context.Background(), cfg, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	block.Store(true)
	if err := a.notify.Enqueue(t.Context(), id, request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("重启后未恢复渠道")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("重启重放或取消重试: calls=%d", calls.Load())
	}
	a, err = open(context.Background(), cfg, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	var n, ok, attempts int
	if err := a.db.Read().QueryRow("SELECT count(*) FROM notify_logs").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if err := a.db.Read().QueryRow("SELECT ok,attempts FROM notify_logs ORDER BY id DESC LIMIT 1").Scan(&ok, &attempts); err != nil || ok != 0 || attempts != 1 {
		t.Fatal(ok, attempts, err)
	}
}
