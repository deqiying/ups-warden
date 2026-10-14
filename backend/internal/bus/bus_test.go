package bus

import (
	"testing"
	"ups-warden/internal/model"
)

func TestIsolationOverflowAndClose(t *testing.T) {
	var b Bus
	slow, cancel := b.Subscribe()
	fast, stop := b.Subscribe()
	defer stop()
	u := model.DeviceUpdate{State: model.DeviceState{Fields: map[string]model.Field{"x": {Num: 1}}}, Events: []model.Event{{Summary: map[string]model.Field{"x": {Num: 2}}}}}
	b.Publish(u)
	a := <-fast
	a.State.Fields["x"] = model.Field{Num: 9}
	a.Events[0].Summary["x"] = model.Field{Num: 9}
	v := <-slow
	if v.State.Fields["x"].Num != 1 || v.Events[0].Summary["x"].Num != 2 || u.State.Fields["x"].Num != 1 {
		t.Fatal("订阅共享引用")
	}
	for i := 0; i < 257; i++ {
		b.Publish(u)
		<-fast
	}
	if b.Dropped() != 1 {
		t.Fatal("未关闭慢订阅者")
	}
	n := 0
	for range slow {
		n++
	}
	if n != 256 {
		t.Fatal(n)
	}
	cancel()
	cancel()
	b.Close()
	b.Close()
	if _, ok := <-fast; ok {
		t.Fatal("关闭后仍可接收")
	}
	closed, c := b.Subscribe()
	defer c()
	if _, ok := <-closed; ok {
		t.Fatal("关闭后订阅未关闭")
	}
	b.Publish(u)
}
