package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/sim"
)

// 压测各阶段时长：预热后测空闲 CPU/内存，再测 SSE 延迟，最后全部设备同时停电测通知延迟。
const (
	loadWarmup = 20 * time.Second
	loadIdle   = 30 * time.Second
	loadSSE    = 15 * time.Second
)

type simAddrs struct{ SNMP, Modbus, AYI9 string }

// TestLoadSimHost 是 TestLoad 的模拟器子进程入口，使模拟器的 CPU/内存不计入被测服务。
// 每台模拟设备监听独立的 loopback 地址（同 IP 串行约束不互相影响）；
// stdin 收到 "outage" 后全部进入停电，stdin 关闭时退出。
func TestLoadSimHost(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("UPS_WARDEN_LOAD_SIM_HOST"))
	if n == 0 {
		t.Skip("仅作为 TestLoad 的模拟器子进程运行")
	}
	var offset atomic.Int64
	now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	scenario := sim.Scenario{Steps: []sim.Step{{At: "1h", State: "outage"}}}
	addrs := make([]simAddrs, n)
	for i := range n {
		host := fmt.Sprintf("127.0.%d.%d:0", i/250, i%250+2)
		s, err := sim.Start(sim.Config{ET1K: true, Scenario: scenario, Now: now, SNMPAddress: host, ModbusAddress: host, AYI9Address: host})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		addrs[i].SNMP, addrs[i].Modbus, addrs[i].AYI9 = s.Addresses()
	}
	data, err := json.Marshal(addrs)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SIMADDR %s\n", data)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() == "outage" {
			offset.Store(int64(time.Hour))
		}
	}
}

// TestLoad 按 UPS_WARDEN_LOAD（逗号分隔的设备数）压测 3 s 轮询下的资源与时效（NFR-01~03）。
func TestLoad(t *testing.T) {
	spec := os.Getenv("UPS_WARDEN_LOAD")
	if spec == "" {
		t.Skip("设置 UPS_WARDEN_LOAD=32,100 运行压测（mise run backend:load）")
	}
	for _, item := range strings.Split(spec, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || n < 1 || n > 250 {
			t.Fatal("UPS_WARDEN_LOAD 须为 1～250 的逗号分隔整数")
		}
		t.Run(strconv.Itoa(n), func(t *testing.T) { runLoad(t, n) })
	}
}

func runLoad(t *testing.T, n int) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestLoadSimHost$", "-test.count=1")
	cmd.Env = append(os.Environ(), "UPS_WARDEN_LOAD_SIM_HOST="+strconv.Itoa(n))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Error("模拟器子进程:", err)
		}
	})
	var addrs []simAddrs
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "SIMADDR "); ok {
			if err = json.Unmarshal([]byte(data), &addrs); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	go io.Copy(io.Discard, stdout)
	if len(addrs) != n {
		t.Fatal("模拟器子进程未就绪")
	}

	s := startServer(t)
	url, deliveries := startWebhook(t, 2*n)
	channel := s.create("/channels", map[string]any{"name": "load", "type": "webhook", "enabled": true, "url": url, "config": map[string]any{"body_template": `{"event":"{{event.type}}","device":"{{device.name}}"}`}})
	notify := s.create("/actions", map[string]any{"name": "notify", "type": "notify", "config": map[string]any{"channel_ids": []int64{channel}}})
	s.create("/rules", map[string]any{"name": "停电通知", "enabled": true, "dry_run": false, "scope": map[string]any{"type": "all"}, "trigger": map[string]any{"type": "event", "events": []string{"mains_lost"}}, "actions": []map[string]any{{"action_id": notify}}})
	names := map[string]int64{}
	started := time.Now()
	for i, a := range addrs {
		name := fmt.Sprintf("d%d", i+1)
		id := s.create("/devices", map[string]any{"name": name})
		names[name] = id
		source := map[string]any{"poll_ms": 3000}
		switch i % 3 {
		case 0:
			source["protocol"], source["profile_key"], source["community"] = "snmp", "et1k-snmp", "sim-public"
			source["host"], source["port"] = hostPort(t, a.SNMP)
		case 1:
			source["protocol"], source["profile_key"] = "modbus", "et1k-modbus"
			source["host"], source["port"] = hostPort(t, a.Modbus)
		default:
			source["protocol"], source["profile_key"] = "ayi9-json", "et1k-ayi9-json"
			source["host"], source["port"] = hostPort(t, a.AYI9)
		}
		s.create(fmt.Sprintf("/devices/%d/sources", id), source)
	}
	for {
		var list struct {
			Items []model.DeviceState `json:"items"`
		}
		s.call("GET", "/snapshots", nil, 200, &list)
		ok := 0
		for _, item := range list.Items {
			if item.CommState == "ok" && item.PowerState == "online" {
				ok++
			}
		}
		if ok == n {
			break
		}
		if time.Since(started) > 60*time.Second {
			t.Fatalf("仅 %d/%d 台设备采集正常", ok, n)
		}
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(time.Until(started.Add(loadWarmup)))

	// 空闲阶段：无 SSE 订阅者，只有采集、状态、历史与规则评估。
	// 先归还初始化/登录时 argon2id（每次 64 MiB）留下的一次性堆峰值，只测采集稳态。
	debug.FreeOSMemory()
	samples := []metrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/released:bytes"}, {Name: "/memory/classes/heap/objects:bytes"}}
	var peakResident, peakHeap uint64
	cpu0, idleStart := processCPU(), time.Now()
	for time.Since(idleStart) < loadIdle {
		metrics.Read(samples)
		peakResident = max(peakResident, samples[0].Value.Uint64()-samples[1].Value.Uint64())
		peakHeap = max(peakHeap, samples[2].Value.Uint64())
		time.Sleep(time.Second)
	}
	core := float64(processCPU()-cpu0) / float64(time.Since(idleStart)) * 100

	// SSE 阶段：采集成功的 device_state 帧（at == last_ok_at）从采集完成到客户端收到的延迟。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := s.stream(ctx, 1<<16)
	var sseLatency []time.Duration
	mainsAt := map[int64]time.Time{}
	handle := func(e sseEvent) {
		switch e.name {
		case "device_state":
			var v struct {
				At       time.Time `json:"at"`
				LastOKAt time.Time `json:"last_ok_at"`
			}
			if err := json.Unmarshal(e.data, &v); err != nil {
				t.Fatal(err)
			}
			if v.At.Equal(v.LastOKAt) {
				sseLatency = append(sseLatency, e.received.Sub(v.At))
			}
		case "event":
			var v model.Event
			if err := json.Unmarshal(e.data, &v); err != nil {
				t.Fatal(err)
			}
			if v.Type == model.EventMainsLost {
				mainsAt[v.DeviceID] = v.At
			}
		}
	}
	timeout := time.After(loadSSE)
	for collecting := true; collecting; {
		select {
		case e := <-frames:
			handle(e)
		case <-timeout:
			collecting = false
		}
	}

	// 停电阶段：全部设备同时停电，测量去抖确认（事件 at）到 Webhook 收到的延迟。
	if _, err = io.WriteString(stdin, "outage\n"); err != nil {
		t.Fatal(err)
	}
	received := map[int64]time.Time{}
	deadline := time.After(60 * time.Second)
	for waiting := true; waiting && (len(received) < n || len(mainsAt) < n); {
		select {
		case e := <-frames:
			handle(e)
		case d := <-deliveries:
			var v struct{ Event, Device string }
			if err := json.Unmarshal([]byte(d.body), &v); err != nil {
				t.Fatal(err)
			}
			received[names[v.Device]] = d.received
		case <-deadline:
			waiting = false
		}
	}
	var notifyLatency []time.Duration
	for id, at := range received {
		if confirmed, ok := mainsAt[id]; ok {
			notifyLatency = append(notifyLatency, at.Sub(confirmed))
		}
	}
	var runs struct {
		Items []struct{ Status string } `json:"items"`
	}
	s.call("GET", "/action-runs?limit=200", nil, 200, &runs)
	statuses := map[string]int{}
	for _, r := range runs.Items {
		statuses[r.Status]++
	}

	t.Logf("设备 %d 台（3 s 轮询，SNMP/Modbus/AYI9 轮换），GOMAXPROCS=%d", n, runtime.GOMAXPROCS(0))
	t.Logf("空闲 %s：CPU %.2f%% 单核（%.3f%% 全部核心）；Go 常驻内存峰值 %.1f MiB，堆对象峰值 %.1f MiB", loadIdle, core, core/float64(runtime.NumCPU()), float64(peakResident)/(1<<20), float64(peakHeap)/(1<<20))
	t.Logf("SSE 延迟（采集完成→客户端）样本 %d：%s", len(sseLatency), summary(sseLatency))
	t.Logf("停电：mains_lost %d/%d，Webhook 送达 %d/%d，动作结果 %v；通知延迟（确认→送达）%s", len(mainsAt), n, len(received), n, statuses, summary(notifyLatency))

	if n <= 32 && peakResident > 128<<20 {
		t.Errorf("NFR-02：32 台以内常驻内存 %.1f MiB 超过 128 MiB", float64(peakResident)/(1<<20))
	}
	if len(sseLatency) == 0 || percentile(sseLatency, 0.99) > time.Second {
		t.Errorf("NFR-03：SSE 延迟 p99 超过 1 s 或无样本")
	}
	if len(mainsAt) != n || len(notifyLatency) != n {
		t.Errorf("AC-05/NFR-03：%d 台设备中仅 %d 台确认停电、%d 台送达通知", n, len(mainsAt), len(notifyLatency))
	}
	if len(notifyLatency) > 0 && slices.Max(notifyLatency) > 2*time.Second {
		t.Errorf("NFR-03：通知延迟最大 %s 超过 2 s", slices.Max(notifyLatency))
	}
}

func percentile(values []time.Duration, p float64) time.Duration {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[min(len(sorted)-1, int(float64(len(sorted))*p))]
}

func summary(values []time.Duration) string {
	if len(values) == 0 {
		return "无样本"
	}
	return fmt.Sprintf("p50 %s / p99 %s / max %s", percentile(values, 0.5).Round(time.Millisecond), percentile(values, 0.99).Round(time.Millisecond), slices.Max(values).Round(time.Millisecond))
}
