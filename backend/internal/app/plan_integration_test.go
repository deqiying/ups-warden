package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"ups-warden/internal/action"
	"ups-warden/internal/collector"
	"ups-warden/internal/config"
	"ups-warden/internal/host"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/plan"
	"ups-warden/internal/profile"
	"ups-warden/internal/rule"
	"ups-warden/internal/secret"
	"ups-warden/internal/sim"
	"ups-warden/internal/state"
)

// m7 是 M7 集成测试夹具：临时数据目录上的完整 app、设备 1 与指向本机假 Webhook 的渠道 1。
type m7 struct {
	a          *App
	cfg        config.Config
	deliveries chan string
}

func newM7(t *testing.T) *m7 {
	t.Helper()
	m := &m7{cfg: config.Config{DataDir: t.TempDir()}, deliveries: make(chan string, 64)}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		m.deliveries <- string(body)
		w.WriteHeader(204)
	}))
	t.Cleanup(target.Close)
	m.open(t)
	t.Cleanup(func() {
		if err := m.a.Close(); err != nil {
			t.Error(err)
		}
	})
	pipelineSQL(t, m.a.db, `INSERT INTO devices(id,name) VALUES(1,'M7 loopback')`)
	channel := notify.ChannelRecord{Name: "loopback", Type: "webhook", Enabled: true, Credentials: notify.Credentials{URL: target.URL}}
	notify.NormalizeConfig(channel.Type, &channel.Config)
	encoded, encrypted, err := notify.EncodeChannel(channel, m.a.cipher)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.a.notify.Change(t.Context(), 0, func() (int64, error) {
		res, e := m.a.db.Write().Exec("INSERT INTO channels(name,type,config,secret,enabled) VALUES(?,?,?,?,1)", channel.Name, channel.Type, encoded, encrypted)
		if e != nil {
			return 0, e
		}
		return res.LastInsertId()
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *m7) open(t *testing.T) {
	t.Helper()
	a, err := open(context.Background(), m.cfg, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	m.a = a
}

// reopen 模拟进程重启：关闭 app 后在同一数据目录重建。
func (m *m7) reopen(t *testing.T) {
	t.Helper()
	if err := m.a.Close(); err != nil {
		t.Fatal(err)
	}
	m.open(t)
}

func (m *m7) insert(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	res, err := m.a.db.Write().Exec(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (m *m7) action(t *testing.T, r action.Record) int64 {
	t.Helper()
	action.Normalize(&r)
	if err := action.Validate(r); err != nil {
		t.Fatal(err)
	}
	cfg, encrypted, err := action.Encode(r, m.a.cipher)
	if err != nil {
		t.Fatal(err)
	}
	return m.insert(t, "INSERT INTO actions(name,type,timeout_s,config,secret) VALUES(?,?,?,?,?)", r.Name, r.Type, r.TimeoutS, cfg, encrypted)
}

func (m *m7) http(t *testing.T, url string) int64 {
	t.Helper()
	return m.action(t, action.Record{Name: "http " + url, Type: "http", Credentials: action.Credentials{URL: secret.NewString(url)}})
}

func (m *m7) ssh(t *testing.T, h *sshHost) int64 {
	t.Helper()
	conn, encrypted, online, err := host.Encode(h.record, m.a.cipher)
	if err != nil {
		t.Fatal(err)
	}
	id := m.insert(t, "INSERT INTO hosts(name,address,kind,conn,secret,hostkey_fingerprint,online_check) VALUES(?,?,?,?,?,?,?)", h.record.Name, h.record.Address, h.record.Kind, conn, encrypted, h.record.Fingerprint, online)
	return m.action(t, action.Record{Name: "ssh shutdown", Type: "ssh", Config: action.Config{HostID: id, Command: "shutdown -h now", Shutdown: true}})
}

func (m *m7) plan(t *testing.T, r plan.Record) int64 {
	t.Helper()
	r.NotifyChannelIDs = []int64{1}
	plan.Normalize(&r)
	if err := m.a.plans.Validate(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	cfg, err := plan.Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	return m.insert(t, "INSERT INTO plans(name,config) VALUES(?,?)", r.Name, cfg)
}

// rule 创建停电事件规则，触发引用该计划的 plan 动作，返回动作 ID。
func (m *m7) rule(t *testing.T, planID int64) int64 {
	t.Helper()
	id := m.action(t, action.Record{Name: "start plan", Type: "plan", Config: action.Config{PlanID: planID}})
	r := rule.Record{Name: "outage plan", Enabled: true, Scope: rule.Scope{Type: "all"}, Trigger: rule.Trigger{Type: "event", Events: []string{model.EventMainsLost}}, Actions: []rule.ActionRef{{ActionID: id}}}
	scope, trigger, actions, recovery, err := rule.Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	m.insert(t, "INSERT INTO rules(name,enabled,dry_run,scope,trigger,actions,recovery_actions) VALUES(?,1,0,?,?,?,?)", r.Name, scope, trigger, actions, recovery)
	return id
}

// outage 启动本机 ET1K 模拟器与状态 actor：lose 确认停电（开启停电周期），restore 确认市电恢复（结束周期）。
// 更新经 app.publish 同时进入计划服务与规则引擎，与生产链路一致。
func (m *m7) outage(t *testing.T) (lose func() model.DeviceUpdate, restore func()) {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Millisecond)
	var offset atomic.Int64
	server, err := sim.Start(sim.Config{ET1K: true, Scenario: sim.Scenario{Steps: []sim.Step{{At: "1s", State: "outage"}, {At: "20s", State: "recovery"}}}, Now: func() time.Time { return base.Add(time.Duration(offset.Load())) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	_, addr, _ := server.Addresses()
	h, port := loopbackPort(t, addr)
	p, err := profile.Builtin("et1k-modbus")
	if err != nil {
		t.Fatal(err)
	}
	probe := collector.New(nil, nil, nil, collector.Options{Logger: testLogger()})
	t.Cleanup(func() { _ = probe.Close() })
	source := collector.Source{ID: 1, DeviceID: 1, Protocol: p.Protocol, Host: h, Port: port, Profile: p, Timeout: time.Second}
	actor, err := state.NewActor(t.Context(), m.a.db, 1, state.DefaultConfig(), m.a.publish, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	sample := func(second int) model.DeviceUpdate {
		t.Helper()
		offset.Store(int64(time.Duration(second) * time.Second))
		result, err := probe.Test(t.Context(), source)
		if err != nil {
			t.Fatal(err)
		}
		u, err := actor.Process(t.Context(), model.SourceResult{DeviceID: 1, SourceID: 1, At: base.Add(time.Duration(second) * time.Second), Snapshot: result.Snapshot})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	lose = func() model.DeviceUpdate {
		t.Helper()
		sample(0)
		sample(1)
		u := sample(11)
		if u.State.OutageCycleID == nil {
			t.Fatal("停电未确认", u.Events)
		}
		return u
	}
	restore = func() {
		t.Helper()
		if u := sample(20); u.State.OutageCycleID == nil {
			t.Fatal("市电恢复去抖前周期提前结束")
		}
		u, err := actor.Advance(t.Context(), base.Add(80*time.Second))
		if err != nil || u.State.OutageCycleID != nil {
			t.Fatal("市电恢复未确认", err)
		}
	}
	return lose, restore
}

// runOf 等待计划出现运行记录并返回其 ID。
func (m *m7) runOf(t *testing.T, planID int64) int64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var id int64
		err := m.a.db.Read().QueryRow("SELECT id FROM plan_runs WHERE plan_id=?", planID).Scan(&id)
		if err == nil {
			return id
		}
		if !errors.Is(err, sql.ErrNoRows) || time.Now().After(deadline) {
			t.Fatal("计划未启动", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (m *m7) wait(t *testing.T, id int64, cond func(model.PlanRun) bool) model.PlanRun {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		v, err := plan.ReadRun(context.Background(), m.a.db, id)
		if err != nil {
			t.Fatal(err)
		}
		if cond(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待计划运行状态超时: %s %v", v.Status, m7Statuses(v))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// expect 等待假 Webhook 收到全部指定计划事件的通知（顺序不限），返回对应正文。
func (m *m7) expect(t *testing.T, events ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	timeout := time.After(10 * time.Second)
	for len(got) < len(events) {
		select {
		case body := <-m.deliveries:
			for _, event := range events {
				if strings.Contains(body, `"type":"`+event+`"`) {
					got[event] = body
				}
			}
		case <-timeout:
			t.Fatal("计划通知未全部送达", events, got)
		}
	}
	return got
}

func (m *m7) submitPlan(t *testing.T, actionID int64, u model.DeviceUpdate) string {
	t.Helper()
	id, err := m.a.actions.Submit(t.Context(), action.Request{ActionID: actionID, Trigger: action.Trigger{Type: "rule", RuleID: 1}, DeviceName: "M7 loopback", State: u.State, Event: model.Event{DeviceID: 1, Type: model.EventMainsLost, At: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.a.actions.Wait(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func m7Ended(v model.PlanRun) bool { return v.Status != "countdown" && v.Status != "running" }
func m7Statuses(v model.PlanRun) []string {
	out := []string{}
	for _, s := range v.Steps {
		out = append(out, s.Status)
	}
	return out
}

// targets 是本机假 HTTP 受控端：按路径计数，/block 挂起直到请求被取消。
type targets struct {
	*httptest.Server
	mu      sync.Mutex
	calls   map[string]int
	arrived chan string
}

func newTargets(t *testing.T) *targets {
	t.Helper()
	f := &targets{calls: map[string]int{}, arrived: make(chan string, 32)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.calls[r.URL.Path]++
		f.mu.Unlock()
		f.arrived <- r.URL.Path
		if r.URL.Path == "/block" {
			<-r.Context().Done()
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *targets) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *targets) await(t *testing.T, path string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case got := <-f.arrived:
			if got == path {
				return
			}
		case <-timeout:
			t.Fatal("请求未到达假受控端", path)
		}
	}
}

// sshHost 是本机假受控主机：接受 exec 后不回退出码直接断连（模拟关机）；
// down 为真时先关闭监听，使 TCP 在线检测判定离线，否则主机保持在线。
type sshHost struct {
	record   host.Record
	commands chan string
}

func newSSHHost(t *testing.T, down bool) *sshHost {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	h := &sshHost{commands: make(chan string, 8), record: host.Record{Name: "loopback", Address: "127.0.0.1", Kind: "linux", Conn: host.Connection{Port: port, Username: "tester", AuthMethod: "password"}, Credentials: host.Credentials{Password: secret.NewString("test-only-password")}, Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), OnlineCheck: host.OnlineCheck{Type: "tcp", Port: port}}}
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if string(p) != "test-only-password" {
			return nil, errors.New("bad password")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns[conn] = true
			mu.Unlock()
			wg.Go(func() {
				defer conn.Close()
				server, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					_, requests, err := nc.Accept()
					if err != nil {
						return
					}
					for req := range requests {
						var payload struct{ Command string }
						if req.Type != "exec" || ssh.Unmarshal(req.Payload, &payload) != nil {
							_ = req.Reply(false, nil)
							continue
						}
						_ = req.Reply(true, nil)
						h.commands <- payload.Command
						if down {
							_ = listener.Close()
						}
						return
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return h
}

// TestM7OutagePlanStagesOfflineWaitAndNotifications 覆盖 AC-05 计划部分：停电确认 → 规则 → plan 动作 → 倒计时 →
// 阶段一并行 SSH 关机并等待离线（一台离线、一台超时）→ 阶段二 HTTP；进度、动作关联与通知完整，同周期不重复执行。
func TestM7OutagePlanStagesOfflineWaitAndNotifications(t *testing.T) {
	m := newM7(t)
	f := newTargets(t)
	down, stay := newSSHHost(t, true), newSSHHost(t, false)
	sshDown, sshStay, final := m.ssh(t, down), m.ssh(t, stay), m.http(t, f.URL+"/final")
	id := m.plan(t, plan.Record{Name: "AC-05", Config: plan.Config{CountdownS: 1, OnMainsRestored: "cancel_pending", OnRestart: "resume", Stages: []plan.Stage{
		{OfflineWaitS: 1, Steps: []plan.Step{{ActionID: sshDown}, {ActionID: sshStay}}},
		{Steps: []plan.Step{{ActionID: final}}},
	}}})
	planAction := m.rule(t, id)
	updates, cancel := m.a.bus.Subscribe()
	defer cancel()
	lose, _ := m.outage(t)
	u := lose()
	runID := m.runOf(t, id)
	v := m.wait(t, runID, m7Ended)
	if v.Status != "completed_with_errors" || !slices.Equal(m7Statuses(v), []string{"offline", "sent", "sent"}) {
		t.Fatal("阶段执行结果错误", v.Status, m7Statuses(v))
	}
	if v.DryRun || v.OutageCycleID == nil || *v.OutageCycleID != *u.State.OutageCycleID || v.Steps[0].HostID == nil {
		t.Fatal("运行未关联停电周期或受控主机", v)
	}
	if !v.Steps[1].Result.OfflineTimeout || v.Steps[0].Result.OfflineTimeout {
		t.Fatal("等待离线结果错误", v.Steps[0].Result, v.Steps[1].Result)
	}
	if v.Steps[2].StartedAt.Before(*v.Steps[0].EndedAt) || v.Steps[2].StartedAt.Before(*v.CountdownUntil) {
		t.Fatal("阶段未串行或倒计时未生效")
	}
	for _, h := range []*sshHost{down, stay} {
		if got := <-h.commands; got != "shutdown -h now" || len(h.commands) != 0 {
			t.Fatal("SSH 关机命令错误或重复", got)
		}
	}
	if f.count("/final") != 1 {
		t.Fatal("HTTP 步骤执行次数错误", f.count("/final"))
	}
	for _, s := range v.Steps {
		var trigger string
		if err := m.a.db.Read().QueryRow("SELECT trigger FROM action_runs WHERE id=?", s.Result.ActionRunID).Scan(&trigger); err != nil || !strings.Contains(trigger, `"plan_run_id"`) {
			t.Fatal("步骤动作记录未关联计划运行", s, err)
		}
	}
	var started string
	if err := m.a.db.Read().QueryRow("SELECT status FROM action_runs WHERE action_id=?", planAction).Scan(&started); err != nil || started != "started" {
		t.Fatal("规则触发的 plan 动作结果错误", started, err)
	}
	bodies := m.expect(t, "plan_started", "plan_step_failed", "plan_completed")
	if !strings.Contains(bodies["plan_step_failed"], "等待离线超时") {
		t.Fatal("离线超时通知缺少原因", bodies["plan_step_failed"])
	}
	seen := map[string]bool{}
	for drained := false; !drained; {
		select {
		case u, ok := <-updates:
			if !ok {
				t.Fatal("进度订阅被断开")
			}
			if u.PlanProgress != nil && u.PlanProgress.ID == runID {
				seen[u.PlanProgress.Status] = true
			}
		default:
			drained = true
		}
	}
	if !seen["countdown"] || !seen["running"] || !seen["completed_with_errors"] {
		t.Fatal("plan_progress 推送不完整", seen)
	}
	// 同一停电周期再次触发（如规则重新评估）只记录跳过，不再执行。
	if status := m.submitPlan(t, planAction, u); status != "skipped_executed" {
		t.Fatal("同周期重复触发未跳过", status)
	}
	if n := len(down.commands) + len(stay.commands); n != 0 || f.count("/final") != 1 {
		t.Fatal("重复触发产生副作用")
	}
}

// TestM7CountdownCancelledByMainsRestored 覆盖 AC-06：倒计时期间去抖确认的市电恢复自动取消计划并通知，任何步骤都不执行。
func TestM7CountdownCancelledByMainsRestored(t *testing.T) {
	m := newM7(t)
	f := newTargets(t)
	h := newSSHHost(t, true)
	id := m.plan(t, plan.Record{Name: "AC-06", Config: plan.Config{CountdownS: 60, OnMainsRestored: "continue", OnRestart: "resume", Stages: []plan.Stage{
		{Steps: []plan.Step{{ActionID: m.ssh(t, h)}}},
		{Steps: []plan.Step{{ActionID: m.http(t, f.URL+"/final")}}},
	}}})
	planAction := m.rule(t, id)
	lose, restore := m.outage(t)
	u := lose()
	runID := m.runOf(t, id)
	m.wait(t, runID, func(v model.PlanRun) bool { return v.Status == "countdown" })
	m.expect(t, "plan_started")
	restore()
	v := m.wait(t, runID, m7Ended)
	if v.Status != "cancelled" || !slices.Equal(m7Statuses(v), []string{"cancelled", "cancelled"}) {
		t.Fatal("市电恢复未取消倒计时", v.Status, m7Statuses(v))
	}
	if body := m.expect(t, "plan_cancelled")["plan_cancelled"]; !strings.Contains(body, "市电已恢复") {
		t.Fatal("取消通知缺少原因", body)
	}
	// 周期结束后迟到的触发不得启动新运行。
	if status := m.submitPlan(t, planAction, u); status != "skipped_restored" {
		t.Fatal("周期结束后的触发未跳过", status)
	}
	if len(h.commands) != 0 || f.count("/final") != 0 {
		t.Fatal("取消后仍执行步骤")
	}
}

// TestM7RestartResumeAndAbort 覆盖 AC-07：同一数据目录重建 app 后，执行中的步骤记为 unknown 且不重放，
// resume 计划从剩余步骤继续，abort 计划终止；两者均发送通知。
func TestM7RestartResumeAndAbort(t *testing.T) {
	m := newM7(t)
	f := newTargets(t)
	resume := m.plan(t, plan.Record{Name: "resume", Config: plan.Config{OnMainsRestored: "cancel_pending", OnRestart: "resume", Stages: []plan.Stage{
		{Steps: []plan.Step{{ActionID: m.http(t, f.URL+"/ok")}}},
		{Steps: []plan.Step{{ActionID: m.http(t, f.URL+"/block")}}},
		{Steps: []plan.Step{{ActionID: m.http(t, f.URL+"/last")}}},
	}}})
	abort := m.plan(t, plan.Record{Name: "abort", Config: plan.Config{OnMainsRestored: "cancel_pending", OnRestart: "abort", Stages: []plan.Stage{
		{Steps: []plan.Step{{ActionID: m.http(t, f.URL+"/first")}}},
		{DelayS: 60, Steps: []plan.Step{{ActionID: m.http(t, f.URL+"/never")}}},
	}}})
	cycle := m.insert(t, "INSERT INTO outage_cycles(device_id,started_at) VALUES(1,?)", time.Now().UnixMilli())
	req := action.Request{Trigger: action.Trigger{Type: "rule", RuleID: 1}, DeviceName: "M7 loopback", State: model.DeviceState{DeviceID: 1, OutageCycleID: &cycle}}
	var runs []int64
	for _, id := range []int64{resume, abort} {
		run, err := m.a.plans.Start(t.Context(), id, req, nil)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	f.await(t, "/block")
	m.wait(t, runs[1], func(v model.PlanRun) bool { return v.CurrentStage == 1 })
	m.expect(t, "plan_started")
	m.reopen(t)
	v := m.wait(t, runs[0], m7Ended)
	if v.Status != "completed_with_errors" || !slices.Equal(m7Statuses(v), []string{"sent", "unknown", "sent"}) {
		t.Fatal("resume 恢复结果错误", v.Status, m7Statuses(v))
	}
	if a := m.wait(t, runs[1], m7Ended); a.Status != "aborted" || !slices.Equal(m7Statuses(a), []string{"sent", "cancelled"}) {
		t.Fatal("abort 恢复结果错误", a.Status, m7Statuses(a))
	}
	for path, want := range map[string]int{"/ok": 1, "/block": 1, "/last": 1, "/first": 1, "/never": 0} {
		if got := f.count(path); got != want {
			t.Fatalf("重启后重复或遗漏执行 %s: %d", path, got)
		}
	}
	m.expect(t, "plan_resumed", "plan_aborted", "plan_completed")
}
