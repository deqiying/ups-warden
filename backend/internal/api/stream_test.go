package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
)

type streamWriter struct {
	header           http.Header
	body             bytes.Buffer
	status           int
	block            bool
	entered, release chan struct{}
	deadline         time.Time
}

func (w *streamWriter) Header() http.Header { return w.header }
func (w *streamWriter) WriteHeader(n int)   { w.status = n }
func (w *streamWriter) Write(b []byte) (int, error) {
	if w.block {
		w.block = false
		close(w.entered)
		<-w.release
	}
	return w.body.Write(b)
}
func (w *streamWriter) Flush()                              {}
func (w *streamWriter) SetWriteDeadline(at time.Time) error { w.deadline = at; return nil }
func startStream(f *apiFixture) (*streamWriter, context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "http://localhost/api/v1/stream", nil).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: f.token})
	w := &streamWriter{header: http.Header{}}
	done := make(chan struct{})
	go func() { defer close(done); f.s.ServeHTTP(w, r) }()
	return w, cancel, done
}
func TestStreamSyncDeltaHeartbeatAndRevocation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		w, cancel, done := startStream(f)
		defer cancel()
		synctest.Wait()
		if !strings.Contains(w.body.String(), "event: sync\n") || w.header.Get("Content-Type") != "text/event-stream" {
			t.Fatal(w.body.String())
		}
		u := model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, At: time.Now(), Fields: map[string]model.Field{}}, Events: []model.Event{{ID: 1, DeviceID: 1, Type: model.EventMainsLost, At: time.Now()}}}
		f.s.opts.Bus.Publish(u)
		synctest.Wait()
		if !strings.Contains(w.body.String(), "event: device_state") || !strings.Contains(w.body.String(), "event: event") {
			t.Fatal("缺少增量")
		}
		f.s.opts.Bus.Publish(model.DeviceUpdate{Resync: true})
		synctest.Wait()
		if strings.Count(w.body.String(), "event: sync") != 2 {
			t.Fatal("配置变更未重同步")
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		if !strings.Contains(w.body.String(), ": heartbeat") {
			t.Fatal("无心跳")
		}
		if err := f.s.opts.Auth.Logout(context.Background(), f.token, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("注销后实时流未断开")
		}
	})
}
func TestStreamOverflowDisconnects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		w, cancel, done := startStream(f)
		defer cancel()
		synctest.Wait()
		w.block = true
		w.entered = make(chan struct{})
		w.release = make(chan struct{})
		u := model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, At: time.Now()}}
		f.s.opts.Bus.Publish(u)
		<-w.entered
		for i := 0; i < 257; i++ {
			f.s.opts.Bus.Publish(u)
		}
		if f.s.opts.Bus.Dropped() != 1 {
			t.Fatal("慢订阅未溢出")
		}
		close(w.release)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("溢出未断开")
		}
		if got := strings.Count(w.body.String(), "event: device_state"); got != 1 {
			t.Fatalf("溢出后仍发送积压帧: %d", got)
		}
	})
}
func TestRawViewAndRedaction(t *testing.T) {
	raw := driver.RawData{Protocol: driver.ProtocolModbus, Modbus: map[driver.RegKey]uint16{{FC: driver.FC03, Addr: 28}: 2200}, SNMP: map[string]driver.SNMPValue{"test": {Text: "private-community", Bytes: []byte("private-community")}}}
	value, err := redactDiagnostic(rawView(raw), "private-community")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := httptest.NewRecorder()
	reply(w, 200, value)
	out.Write(w.Body.Bytes())
	if strings.Contains(out.String(), "private-community") || strings.Contains(out.String(), "cHJpdmF0ZS1jb21tdW5pdHk=") || !strings.Contains(out.String(), `"3:28":2200`) {
		t.Fatal(out.String())
	}
}

func TestDiagnosticByteSubstringRedaction(t *testing.T) {
	for _, prefix := range []string{"x", "xy", "xyz"} {
		raw := driver.RawData{SNMP: map[string]driver.SNMPValue{"test": {Bytes: []byte(prefix + "private-community" + "suffix")}}}
		value, err := redactDiagnostic(rawView(raw), "private-community")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			SNMP map[string]struct {
				Bytes string `json:"bytes"`
			} `json:"snmp"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(out.SNMP["test"].Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(decoded), "private-community") {
			t.Fatal("字节字段仍泄露凭据")
		}
	}
}
