package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/profile"
)

type snapshotRuntime struct{ states []model.DeviceState }

func (r *snapshotRuntime) Snapshots() []model.DeviceState {
	return append([]model.DeviceState{}, r.states...)
}
func (r *snapshotRuntime) ChangeDevices(_ context.Context, _ []int64, change func() error) error {
	return change()
}

func TestSnapshotOutputUTCAndIsolation(t *testing.T) {
	at := time.Date(2026, 10, 15, 1, 0, 0, 0, time.FixedZone("test", 8*3600))
	input := model.DeviceState{At: at, LastOKAt: at, PendingSince: &at, OnBatterySince: &at}
	out := stateView(input)
	for _, v := range []time.Time{out.At, out.LastOKAt, *out.PendingSince, *out.OnBatterySince} {
		if v.Location() != time.UTC || !v.Equal(at) {
			t.Fatal("时间未规范化为 UTC")
		}
	}
	if input.PendingSince.Location() == time.UTC || input.OnBatterySince.Location() == time.UTC {
		t.Fatal("修改了内部时钟")
	}
	data, err := json.Marshal(profile.Recommendation{Key: "example", Score: 100, Reason: "test"})
	if err != nil || string(data) != `{"key":"example","score":100,"reason":"test"}` {
		t.Fatalf("推荐 JSON 不符合契约: %s %v", data, err)
	}
}

func TestStreamResyncDoesNotReintroduceRemovedDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		runtime := &snapshotRuntime{states: []model.DeviceState{{DeviceID: 1, At: time.Now()}}}
		f.s.opts.Runtime = runtime
		w, cancel, done := startStream(f)
		defer cancel()
		synctest.Wait()
		// 已删除/停用设备不应由重同步之后积压的旧更新复活。
		runtime.states = nil
		f.s.opts.Bus.Publish(model.DeviceUpdate{Resync: true})
		f.s.opts.Bus.Publish(model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, At: time.Now().Add(time.Second)}})
		synctest.Wait()
		if strings.Count(w.body.String(), "event: sync") != 2 || strings.Contains(w.body.String(), "event: device_state") {
			t.Fatal(w.body.String())
		}
		f.s.opts.Bus.Close()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("总线关闭后流未结束")
		}
	})
}

func TestStreamAlertClearWithoutRecoveryEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		f.s.opts.Runtime = &snapshotRuntime{states: []model.DeviceState{{DeviceID: 1, At: time.Now()}}}
		w, cancel, _ := startStream(f)
		defer cancel()
		synctest.Wait()
		f.s.opts.Bus.Publish(model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, At: time.Now()}, AlertsChanged: true})
		synctest.Wait()
		if !strings.Contains(w.body.String(), "event: alert\n") || !strings.Contains(w.body.String(), `"items":[]`) || strings.Contains(w.body.String(), "event: event\n") {
			t.Fatal(w.body.String())
		}
	})
}
