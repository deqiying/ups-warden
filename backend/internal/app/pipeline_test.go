package app

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/bus"
	"ups-warden/internal/collector"
	"ups-warden/internal/config"
	"ups-warden/internal/history"
	"ups-warden/internal/model"
	"ups-warden/internal/profile"
	"ups-warden/internal/sim"
	"ups-warden/internal/state"
	"ups-warden/internal/store"
)

func pipelineSQL(t *testing.T, db *store.Store, q string, args ...any) {
	t.Helper()
	if _, err := db.Write().ExecContext(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func loopbackPort(t *testing.T, address string) (string, int) {
	t.Helper()
	host, text, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(text)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("模拟器目标不是 loopback")
	}
	return host, port
}

// 协议 I/O 使用真实 loopback；物理场景与状态机使用同一显式时钟，不靠真实等待推进去抖。
func TestLoopbackPipelineFlashLostRestart(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Millisecond)
	var offset atomic.Int64
	scenario := sim.Scenario{Steps: []sim.Step{{At: "1s", State: "flash"}, {At: "10s", State: "outage"}, {At: "21s", State: "offline"}, {At: "30s", State: "online"}, {At: "31s", State: "low"}, {At: "40s", State: "recovery"}}}
	server, err := sim.Start(sim.Config{Scenario: scenario, ET1K: true, Now: func() time.Time { return base.Add(time.Duration(offset.Load())) }})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, address, _ := server.Addresses()
	host, port := loopbackPort(t, address)
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pipelineSQL(t, db, "INSERT INTO devices(id,name) VALUES(1,'sim')")
	writer, err := history.New(db, history.Options{Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	var updates bus.Bus
	defer updates.Close()
	events, unsubscribe := updates.Subscribe()
	defer unsubscribe()
	publish := func(u model.DeviceUpdate) {
		if u.Sample {
			writer.Enqueue(u.State)
		}
		updates.Publish(u)
	}
	actor, err := state.NewActor(context.Background(), db, 1, state.DefaultConfig(), publish, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	probe := collector.New(nil, nil, nil, collector.Options{Logger: testLogger()})
	defer probe.Close()
	p, err := profile.Builtin("et1k-modbus")
	if err != nil {
		t.Fatal(err)
	}
	source := collector.Source{ID: 1, DeviceID: 1, Protocol: p.Protocol, Host: host, Port: port, Profile: p, Timeout: 100 * time.Millisecond}
	sample := func(second int) model.DeviceUpdate {
		t.Helper()
		offset.Store(int64(time.Duration(second) * time.Second))
		r, e := probe.Test(context.Background(), source)
		u, err := actor.Process(context.Background(), model.SourceResult{DeviceID: 1, SourceID: 1, At: base.Add(time.Duration(second) * time.Second), Snapshot: r.Snapshot, Failed: e != nil})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	if u := sample(0); u.State.PowerState != "online" {
		t.Fatal("基线错误")
	}
	sample(1)
	if u := sample(6); u.State.PowerState != "online" || len(u.Events) != 0 || u.State.OnBatterySince != nil {
		t.Fatal("AC-04 闪断误报")
	}
	sample(10)
	u := sample(20)
	if u.State.PowerState != "on_battery" || len(u.Events) != 1 {
		t.Fatal("持续停电未确认")
	}
	cycle := *u.State.OutageCycleID
	sample(21)
	sample(22)
	u = sample(23)
	if u.State.CommState != "lost" || u.State.PowerState != "on_battery" || u.State.Fields["battery_capacity_pct"].Quality != model.Stale {
		t.Fatal("AC-08 hold/质量错误")
	}
	u, err = actor.Advance(context.Background(), base.Add(29*time.Second))
	if err != nil || u.State.Fields["on_battery_elapsed"].Num != 19 {
		t.Fatal("失联放电计时未延续", err)
	}
	actor, err = state.NewActor(context.Background(), db, 1, state.DefaultConfig(), publish, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	u = sample(30)
	if u.State.OutageCycleID == nil || *u.State.OutageCycleID != cycle {
		t.Fatal("重启新建周期")
	}
	for _, e := range u.Events {
		if e.Type == model.EventMainsLost {
			t.Fatal("重启重复告警")
		}
	}
	u = sample(31)
	low := false
	for _, event := range u.Events {
		if event.Type == model.EventBatteryLow {
			low = true
		}
	}
	if !low {
		t.Fatal("模拟低电量未触发告警")
	}
	sample(40)
	u, err = actor.Advance(context.Background(), base.Add(100*time.Second))
	if err != nil || u.State.PowerState != "online" || u.State.OutageCycleID != nil {
		t.Fatal("恢复未关闭周期", err)
	}
	// 市电恢复不等于容量恢复；充电越过回差阈值后才清除低电告警。
	u = sample(500)
	cleared := false
	for _, event := range u.Events {
		if event.Type == model.EventBatteryLowCleared {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("充电越过阈值后未清除低电告警")
	}
	if err = writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = writer.Aggregate(context.Background(), base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	result, err := writer.Query(context.Background(), history.Query{DeviceID: 1, From: base, To: base.Add(2 * time.Minute), Resolution: "raw"})
	if err != nil || len(result.Points) != 8 {
		t.Fatalf("历史未排除失联和 tick: %d %v", len(result.Points), err)
	}
	var total int
	if err = db.Read().QueryRow("SELECT count(*) FROM events").Scan(&total); err != nil || total != 6 {
		t.Fatal("事件数量", total, err)
	}
	rows, err := db.Read().Query("SELECT data FROM history_sample UNION ALL SELECT summary FROM events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"QAD", "RTD", "register", "oid", "community", "sim-public", "127.0.0.1"} {
			if strings.Contains(data, forbidden) {
				t.Fatal("协议原始数据入库")
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err = writer.Cleanup(context.Background(), base.Add(366*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = db.Read().QueryRow("SELECT count(*) FROM events").Scan(&total); err != nil || total != 0 {
		t.Fatal("恢复事件未按保留期清理", total, err)
	}
	select {
	case <-events:
	default:
		t.Fatal("更新未发布")
	}
}

func TestAppCollectorAndShutdownFlush(t *testing.T) {
	server, err := sim.Start(sim.Config{ET1K: true})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, address, _ := server.Addresses()
	host, port := loopbackPort(t, address)
	c := config.Config{DataDir: t.TempDir()}
	seed, err := open(context.Background(), c, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	pipelineSQL(t, seed.db, "INSERT INTO devices(id,name) VALUES(1,'sim')")
	pipelineSQL(t, seed.db, `INSERT INTO sources(id,device_id,protocol,host,port,profile_key,profile_version,poll_ms,timeout_ms) VALUES(1,1,'modbus',?,?,'et1k-modbus',1,200,100)`, host, port)
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := open(context.Background(), c, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ch, cancel := a.bus.Subscribe()
	defer cancel()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case u := <-ch:
			if !u.Sample {
				continue
			}
			if u.State.PowerState != "online" || u.State.CommState != "ok" {
				t.Fatal("主服务采集状态错误")
			}
			goto collected
		case <-timeout.C:
			t.Fatal("主服务未采集")
		}
	}
collected:
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), filepath.Join(c.DataDir, "ups-warden.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.Read().QueryRow("SELECT count(*) FROM history_sample").Scan(&n); err != nil || n == 0 {
		t.Fatal("关闭未刷新历史", n, err)
	}
	var last int64
	if err = db.Read().QueryRow("SELECT last_ok_at FROM device_state WHERE device_id=1").Scan(&last); err != nil || last <= 0 {
		t.Fatal("未保存最后成功时间", err)
	}
}
