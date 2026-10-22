package plan

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/bus"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// env 模拟一个进程：通知与数据库跨“重启”保留，动作与计划服务可关闭后在同一数据库上重建。
type env struct {
	dir     string
	db      *store.Store
	cipher  *secret.Cipher
	bus     *bus.Bus
	notify  *notify.Service
	logger  *slog.Logger
	actions *action.Service
	plans   *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "plan.db"))
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	n, err := notify.New(context.Background(), db, cipher, b, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := n.Close(); err != nil {
			t.Error(err)
		}
	})
	e := &env{dir: dir, db: db, cipher: cipher, bus: b, notify: n, logger: logger}
	e.start(t)
	t.Cleanup(func() { e.stop(t) })
	sqlExec(t, db, "INSERT INTO devices(id,name) VALUES(1,'loopback')")
	return e
}
func (e *env) start(t *testing.T) {
	t.Helper()
	a, err := action.New(context.Background(), e.db, e.cipher, e.notify, e.dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(context.Background(), e.db, e.cipher, a, e.notify, e.bus, e.logger)
	if err != nil {
		t.Fatal(err)
	}
	a.SetPlans(p)
	e.actions, e.plans = a, p
}

// stop 按 app 关闭顺序先停计划再停动作。
func (e *env) stop(t *testing.T) {
	t.Helper()
	if e.plans == nil {
		return
	}
	e.plans.Close()
	if err := e.actions.Close(); err != nil {
		t.Error(err)
	}
	e.plans, e.actions = nil, nil
}
func (e *env) restart(t *testing.T) {
	t.Helper()
	e.stop(t)
	e.start(t)
}
func sqlExec(t *testing.T, db *store.Store, q string, args ...any) int64 {
	t.Helper()
	res, err := db.Write().Exec(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (e *env) addHTTP(t *testing.T, url string) int64 {
	t.Helper()
	r := action.Record{Name: "http " + url, Type: "http", Credentials: action.Credentials{URL: secret.NewString(url)}}
	action.Normalize(&r)
	cfg, encrypted, err := action.Encode(r, e.cipher)
	if err != nil {
		t.Fatal(err)
	}
	return sqlExec(t, e.db, "INSERT INTO actions(name,type,timeout_s,config,secret) VALUES(?,?,?,?,?)", r.Name, r.Type, r.TimeoutS, cfg, encrypted)
}
func (e *env) addPlan(t *testing.T, r Record) int64 {
	t.Helper()
	if r.Name == "" {
		r.Name = "测试计划"
	}
	if r.OnMainsRestored == "" {
		r.OnMainsRestored = "cancel_pending"
	}
	if r.OnRestart == "" {
		r.OnRestart = "resume"
	}
	Normalize(&r)
	if err := e.plans.Validate(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	cfg, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	return sqlExec(t, e.db, "INSERT INTO plans(name,config) VALUES(?,?)", r.Name, cfg)
}
func (e *env) cycle(t *testing.T) *int64 {
	t.Helper()
	id := sqlExec(t, e.db, "INSERT INTO outage_cycles(device_id,started_at) VALUES(1,?)", time.Now().UnixMilli())
	return &id
}
func (e *env) start1(planID int64, dry bool, cycle *int64) (int64, error) {
	req := action.Request{DryRun: dry, Trigger: action.Trigger{Type: "manual", UserID: 1}, DeviceName: "loopback", State: model.DeviceState{DeviceID: 1, OutageCycleID: cycle}}
	return e.plans.Start(context.Background(), planID, req, nil)
}
func (e *env) mustStart(t *testing.T, planID int64, dry bool, cycle *int64) int64 {
	t.Helper()
	id, err := e.start1(planID, dry, cycle)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (e *env) wait(t *testing.T, id int64, cond func(model.PlanRun) bool) model.PlanRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := ReadRun(context.Background(), e.db, id)
		if err != nil {
			t.Fatal(err)
		}
		if cond(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待运行状态超时: %+v", v)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func ended(v model.PlanRun) bool { return v.Status != "countdown" && v.Status != "running" }
func statuses(v model.PlanRun) []string {
	out := []string{}
	for _, s := range v.Steps {
		out = append(out, s.Status)
	}
	return out
}

// fakeHTTP 是本机假受控端：/pa 与 /pb 互相等待以证明阶段内并行，/fail 返回 500，/block 挂起直到释放或请求取消。
type fakeHTTP struct {
	*httptest.Server
	mu      sync.Mutex
	order   []string
	arrived chan string
	pair    sync.WaitGroup
	block   chan struct{}
	once    sync.Once
	hook    func(path string)
}

func newFakeHTTP(t *testing.T) *fakeHTTP {
	t.Helper()
	f := &fakeHTTP{arrived: make(chan string, 64), block: make(chan struct{})}
	f.pair.Add(2)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.order = append(f.order, r.URL.Path)
		hook := f.hook
		f.mu.Unlock()
		if hook != nil {
			hook(r.URL.Path)
		}
		f.arrived <- r.URL.Path
		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(500)
		case "/pa", "/pb":
			f.pair.Done()
			done := make(chan struct{})
			go func() { f.pair.Wait(); close(done) }()
			select {
			case <-done:
				w.WriteHeader(204)
			case <-time.After(3 * time.Second):
				w.WriteHeader(500)
			}
		case "/block":
			select {
			case <-f.block:
			case <-r.Context().Done():
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(204)
		}
	}))
	t.Cleanup(func() { f.release(); f.Close() })
	return f
}
func (f *fakeHTTP) release() { f.once.Do(func() { close(f.block) }) }
func (f *fakeHTTP) await(t *testing.T, path string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
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
func (f *fakeHTTP) calls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.order {
		if p == path {
			n++
		}
	}
	return n
}

func steps(ids ...int64) []Step {
	out := []Step{}
	for _, id := range ids {
		out = append(out, Step{ActionID: id})
	}
	return out
}

func TestValidateStructureAndReferences(t *testing.T) {
	e := newEnv(t)
	ok := e.addHTTP(t, "http://127.0.0.1:9/ok")
	notifyAction := sqlExec(t, e.db, `INSERT INTO actions(name,type,timeout_s,config) VALUES('n','notify',30,'{"channel_ids":[1]}')`)
	base := func() Record {
		r := Record{Name: "p", Config: DefaultConfig()}
		r.Stages = []Stage{{Steps: steps(ok)}}
		Normalize(&r)
		return r
	}
	if r := base(); r.Stages[0].StepTimeoutS != 30 || e.plans.Validate(t.Context(), r) != nil {
		t.Fatal("默认单步超时或合法计划校验失败", r)
	}
	for name, mutate := range map[string]func(*Record){
		"empty name":        func(r *Record) { r.Name = " " },
		"no stages":         func(r *Record) { r.Stages = nil },
		"empty stage":       func(r *Record) { r.Stages = []Stage{{StepTimeoutS: 30}} },
		"countdown":         func(r *Record) { r.CountdownS = 3601 },
		"restore policy":    func(r *Record) { r.OnMainsRestored = "ignore" },
		"restart policy":    func(r *Record) { r.OnRestart = "replay" },
		"duplicate action":  func(r *Record) { r.Stages = append(r.Stages, Stage{StepTimeoutS: 30, Steps: steps(ok)}) },
		"duplicate channel": func(r *Record) { r.NotifyChannelIDs = []int64{1, 1} },
		"step timeout":      func(r *Record) { r.Stages[0].StepTimeoutS = 3601 },
		"self not last": func(r *Record) {
			r.Stages = []Stage{{StepTimeoutS: 30, SelfShutdown: true, Steps: steps(ok)}, {StepTimeoutS: 30, Steps: steps(ok + 100)}}
		},
		"self waits offline": func(r *Record) { r.Stages[0].SelfShutdown, r.Stages[0].OfflineWaitS = true, 10 },
		"self two steps":     func(r *Record) { r.Stages[0].SelfShutdown, r.Stages[0].Steps = true, steps(ok, ok+1) },
		"notify step":        func(r *Record) { r.Stages[0].Steps = steps(notifyAction) },
		"missing action":     func(r *Record) { r.Stages[0].Steps = steps(9999) },
		"missing channel":    func(r *Record) { r.NotifyChannelIDs = []int64{42} },
	} {
		r := base()
		mutate(&r)
		if err := e.plans.Validate(t.Context(), r); !errors.Is(err, ErrInvalid) {
			t.Fatal("非法计划未返回 ErrInvalid:", name, err)
		}
	}
	// 持久化加载边界严格解码：未知字段与非法配置都拒绝。
	sqlExec(t, e.db, `INSERT INTO plans(name,config) VALUES('bad','{"stages":[],"extra":1}')`)
	if _, err := Read(t.Context(), e.db, 1); err == nil {
		t.Fatal("非法持久化计划被接受")
	}
}

func TestStagesSerialParallelTimeoutAndFailureDoNotBlock(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	pa, pb, fail := e.addHTTP(t, f.URL+"/pa"), e.addHTTP(t, f.URL+"/pb"), e.addHTTP(t, f.URL+"/fail")
	hang, last := e.addHTTP(t, f.URL+"/block"), e.addHTTP(t, f.URL+"/ok")
	id := e.addPlan(t, Record{Config: Config{CountdownS: 0, Stages: []Stage{
		{Steps: steps(pa, pb, fail)},
		{DelayS: 1, StepTimeoutS: 1, Steps: steps(hang)},
		{Steps: steps(last)},
	}}})
	updates, cancel := e.bus.Subscribe()
	defer cancel()
	runID := e.mustStart(t, id, false, nil)
	v := e.wait(t, runID, ended)
	if v.Status != "completed_with_errors" || !slices.Equal(statuses(v), []string{"sent", "sent", "failed", "failed", "sent"}) {
		t.Fatalf("失败步骤阻塞后续阶段或状态错误: %s %v", v.Status, statuses(v))
	}
	if v.Steps[3].Result.ActionStatus != "timeout" {
		t.Fatal("单步超时未覆盖动作超时", v.Steps[3].Result)
	}
	if v.Steps[3].StartedAt.Sub(*v.Steps[0].EndedAt) < time.Second {
		t.Fatal("阶段开始前延迟未生效")
	}
	f.mu.Lock()
	order := slices.Clone(f.order)
	f.mu.Unlock()
	if len(order) != 5 || order[3] != "/block" || order[4] != "/ok" {
		t.Fatal("阶段未串行执行", order)
	}
	var trigger string
	if err := e.db.Read().QueryRow("SELECT trigger FROM action_runs WHERE id=?", v.Steps[4].Result.ActionRunID).Scan(&trigger); err != nil || !strings.Contains(trigger, `"plan_run_id"`) {
		t.Fatal("步骤动作记录未关联计划运行", trigger, err)
	}
	progress := 0
	for drained := false; !drained; {
		select {
		case u, ok := <-updates:
			if !ok {
				t.Fatal("进度订阅被断开")
			}
			if u.PlanProgress != nil && u.PlanProgress.ID == runID {
				progress++
			}
		default:
			drained = true
		}
	}
	if progress < 5 {
		t.Fatal("进度推送过少", progress)
	}
}

func TestDryRunHasNoSideEffects(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	id := e.addPlan(t, Record{Config: Config{Stages: []Stage{{OfflineWaitS: 60, Steps: steps(e.addHTTP(t, f.URL+"/ok"))}}}})
	v := e.wait(t, e.mustStart(t, id, true, nil), ended)
	if v.Status != "completed" || !v.DryRun || v.Steps[0].Result.ActionStatus != "dry_run" || f.calls("/ok") != 0 {
		t.Fatal("dry-run 产生副作用", v, f.calls("/ok"))
	}
}

func TestCycleIdempotencyAndSingleActiveRun(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	id := e.addPlan(t, Record{Config: Config{CountdownS: 60, Stages: []Stage{{Steps: steps(e.addHTTP(t, f.URL+"/ok"))}}}})
	cycle := e.cycle(t)
	first := e.mustStart(t, id, false, cycle)
	if v := e.wait(t, first, func(v model.PlanRun) bool { return true }); v.Status != "countdown" || v.CountdownUntil == nil || *v.OutageCycleID != *cycle {
		t.Fatal(v)
	}
	for _, dry := range []bool{false, true} {
		if _, err := e.start1(id, dry, cycle); !errors.Is(err, action.ErrPlanActive) {
			t.Fatal("同一计划出现第二个活动运行", dry, err)
		}
	}
	if err := e.plans.Cancel(t.Context(), first, nil); err != nil {
		t.Fatal(err)
	}
	if v := e.wait(t, first, ended); v.Status != "cancelled" || !slices.Equal(statuses(v), []string{"cancelled"}) {
		t.Fatal(v)
	}
	if err := e.plans.Cancel(t.Context(), first, nil); !errors.Is(err, ErrFinished) {
		t.Fatal(err)
	}
	if err := e.plans.Cancel(t.Context(), 999, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := e.start1(id, false, cycle); !errors.Is(err, action.ErrPlanExecuted) {
		t.Fatal("同一停电周期重复真实执行", err)
	}
	// dry-run 与无周期的手动运行不占用周期约束。
	for _, c := range []*int64{cycle, nil} {
		run := e.mustStart(t, id, c != nil, c)
		if err := e.plans.Cancel(t.Context(), run, nil); err != nil {
			t.Fatal(err)
		}
		e.wait(t, run, ended)
	}
	sqlExec(t, e.db, "UPDATE outage_cycles SET ended_at=? WHERE id=?", time.Now().UnixMilli(), *cycle)
	next := e.cycle(t)
	sqlExec(t, e.db, "UPDATE outage_cycles SET ended_at=? WHERE id=?", time.Now().UnixMilli(), *next)
	if _, err := e.start1(id, false, next); !errors.Is(err, action.ErrPlanCycleEnded) {
		t.Fatal("已结束周期仍启动", err)
	}
	if f.calls("/ok") != 0 {
		t.Fatal("倒计时取消后仍执行")
	}
}

func TestMainsRestoredCountdownAndRunningPolicies(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	cycle := e.cycle(t)
	restored := model.DeviceUpdate{State: model.DeviceState{DeviceID: 1}}
	// 倒计时期间市电恢复总是取消，与 on_mains_restored 无关。
	countdown := e.addPlan(t, Record{Config: Config{CountdownS: 60, OnMainsRestored: "continue", Stages: []Stage{{Steps: steps(e.addHTTP(t, f.URL+"/never"))}}}})
	run := e.mustStart(t, countdown, false, cycle)
	e.plans.Observe(model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, OutageCycleID: cycle}})
	e.plans.Observe(model.DeviceUpdate{State: model.DeviceState{DeviceID: 2}})
	time.Sleep(50 * time.Millisecond)
	if v := e.wait(t, run, func(model.PlanRun) bool { return true }); v.Status != "countdown" {
		t.Fatal("同周期或其他设备的更新误取消", v.Status)
	}
	e.plans.Observe(restored)
	if v := e.wait(t, run, ended); v.Status != "cancelled" {
		t.Fatal(v)
	}
	for _, policy := range []string{"cancel_pending", "continue"} {
		t.Run(policy, func(t *testing.T) {
			f.mu.Lock()
			f.order = nil
			f.mu.Unlock()
			f.block = make(chan struct{})
			f.once = sync.Once{}
			id := e.addPlan(t, Record{Name: policy, Config: Config{OnMainsRestored: policy, Stages: []Stage{
				{Steps: steps(e.addHTTP(t, f.URL+"/block?"+policy))},
				{Steps: steps(e.addHTTP(t, f.URL+"/ok?"+policy))},
			}}})
			run := e.mustStart(t, id, false, cycle)
			f.await(t, "/block")
			e.plans.Observe(restored)
			time.Sleep(50 * time.Millisecond)
			f.release()
			v := e.wait(t, run, ended)
			want := []string{"sent", "cancelled"}
			status := "cancelled"
			if policy == "continue" {
				want, status = []string{"sent", "sent"}, "completed"
			}
			if v.Status != status || !slices.Equal(statuses(v), want) {
				t.Fatal("执行中市电恢复处理错误", v.Status, statuses(v))
			}
		})
	}
}

func TestRestartNeverReplaysAndResumesRemaining(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	id := e.addPlan(t, Record{Config: Config{Stages: []Stage{
		{Steps: steps(e.addHTTP(t, f.URL+"/ok"))},
		{Steps: steps(e.addHTTP(t, f.URL+"/block"))},
		{DelayS: 2, Steps: steps(e.addHTTP(t, f.URL+"/last"))},
	}}})
	run := e.mustStart(t, id, false, e.cycle(t))
	// 中断点一：第一步已发送、第二步执行中。
	f.await(t, "/block")
	e.restart(t)
	// 中断点二：第三阶段延迟期间（步骤尚未开始）。
	e.wait(t, run, func(v model.PlanRun) bool { return v.CurrentStage == 2 })
	e.restart(t)
	v := e.wait(t, run, ended)
	if v.Status != "completed_with_errors" || !slices.Equal(statuses(v), []string{"sent", "unknown", "sent"}) {
		t.Fatal(v.Status, statuses(v))
	}
	if !strings.Contains(v.Steps[1].Result.Error, "结果未知") || v.Steps[1].Result.ActionRunID == 0 {
		t.Fatal("unknown 步骤缺少说明或动作关联", v.Steps[1].Result)
	}
	for path, want := range map[string]int{"/ok": 1, "/block": 1, "/last": 1} {
		if got := f.calls(path); got != want {
			t.Fatalf("重启后重复或遗漏执行 %s: %d", path, got)
		}
	}
}

func TestRestartCountdownAbortAndEndedCycle(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	countdown := e.addPlan(t, Record{Name: "countdown", Config: Config{CountdownS: 2, Stages: []Stage{{Steps: steps(e.addHTTP(t, f.URL+"/countdown"))}}}})
	abort := e.addPlan(t, Record{Name: "abort", Config: Config{OnRestart: "abort", Stages: []Stage{
		{Steps: steps(e.addHTTP(t, f.URL+"/first"))},
		{DelayS: 30, Steps: steps(e.addHTTP(t, f.URL+"/never"))},
	}}})
	ended1 := e.addPlan(t, Record{Name: "ended", Config: Config{CountdownS: 60, Stages: []Stage{{Steps: steps(e.addHTTP(t, f.URL+"/restored"))}}}})
	cont := e.addPlan(t, Record{Name: "continue", Config: Config{OnMainsRestored: "continue", Stages: []Stage{
		{Steps: steps(e.addHTTP(t, f.URL+"/cont1"))},
		{DelayS: 2, Steps: steps(e.addHTTP(t, f.URL+"/cont2"))},
	}}})
	cycle := e.cycle(t)
	other := e.cycle(t)
	c := e.mustStart(t, countdown, false, cycle)
	before := e.wait(t, c, func(model.PlanRun) bool { return true })
	a := e.mustStart(t, abort, false, cycle)
	r := e.mustStart(t, ended1, false, other)
	k := e.mustStart(t, cont, false, other)
	e.wait(t, a, func(v model.PlanRun) bool { return v.CurrentStage == 1 })
	e.wait(t, k, func(v model.PlanRun) bool { return v.CurrentStage == 1 })
	e.stop(t)
	sqlExec(t, e.db, "UPDATE outage_cycles SET ended_at=? WHERE id=?", time.Now().UnixMilli(), *other)
	e.start(t)
	if v := e.wait(t, a, ended); v.Status != "aborted" || !slices.Equal(statuses(v), []string{"sent", "cancelled"}) {
		t.Fatal("abort 策略错误", v.Status, statuses(v))
	}
	if v := e.wait(t, r, ended); v.Status != "cancelled" {
		t.Fatal("周期已结束的运行未取消", v.Status)
	}
	// continue 策略下周期结束不取消执行中的运行，与在线行为一致，按 on_restart 继续。
	if v := e.wait(t, k, ended); v.Status != "completed" || !slices.Equal(statuses(v), []string{"sent", "sent"}) {
		t.Fatal("continue 策略在周期已结束时未继续", v.Status, statuses(v))
	}
	v := e.wait(t, c, ended)
	if v.Status != "completed" || !v.CountdownUntil.Equal(*before.CountdownUntil) || v.Steps[0].StartedAt.Before(*before.CountdownUntil) {
		t.Fatal("倒计时恢复未沿用原截止时刻", v, before.CountdownUntil)
	}
	if f.calls("/never") != 0 || f.calls("/restored") != 0 || f.calls("/first") != 1 || f.calls("/countdown") != 1 || f.calls("/cont1") != 1 || f.calls("/cont2") != 1 {
		t.Fatal("重启恢复执行次数错误", f.order)
	}
}

// 服务关闭后到达的触发（如规则排空时派发的 plan 动作）照常落库但不执行，下次启动恢复，避免触发丢失。
func TestStartAfterCloseIsRecoveredOnRestart(t *testing.T) {
	e := newEnv(t)
	f := newFakeHTTP(t)
	id := e.addPlan(t, Record{Config: Config{Stages: []Stage{{Steps: steps(e.addHTTP(t, f.URL+"/late"))}}}})
	e.plans.Close()
	run := e.mustStart(t, id, false, e.cycle(t))
	if v := e.wait(t, run, func(model.PlanRun) bool { return true }); v.Status != "running" || f.calls("/late") != 0 {
		t.Fatal("关闭后的触发未落库或被立即执行", v.Status, f.calls("/late"))
	}
	e.restart(t)
	if v := e.wait(t, run, ended); v.Status != "completed" || f.calls("/late") != 1 {
		t.Fatal("关闭后落库的运行未在重启后恢复", v.Status, f.calls("/late"))
	}
}

func TestSelfShutdownMarkedBeforeExecution(t *testing.T) {
	for _, path := range []string{"/self", "/fail"} {
		t.Run(path, func(t *testing.T) {
			e := newEnv(t)
			f := newFakeHTTP(t)
			seen := make(chan string, 1)
			f.hook = func(p string) {
				if p != path {
					return
				}
				var run, step string
				if err := e.db.Read().QueryRow("SELECT r.status,s.status FROM plan_runs r JOIN plan_run_steps s ON s.run_id=r.id AND s.stage=1").Scan(&run, &step); err != nil {
					t.Error(err)
				}
				seen <- run + "/" + step
			}
			id := e.addPlan(t, Record{Config: Config{Stages: []Stage{
				{Steps: steps(e.addHTTP(t, f.URL+"/ok"))},
				{SelfShutdown: true, Steps: steps(e.addHTTP(t, f.URL+path))},
			}}})
			run := e.mustStart(t, id, false, nil)
			if got := <-seen; got != "completed/sent" {
				t.Fatal("自关机执行前未先标记运行结束与步骤已发送", got)
			}
			want, step := "completed", "sent"
			if path == "/fail" {
				want, step = "completed_with_errors", "failed"
			}
			v := e.wait(t, run, func(v model.PlanRun) bool { return v.Steps[1].EndedAt != nil })
			if v.Status != want || v.Steps[1].Status != step {
				t.Fatal(v.Status, statuses(v))
			}
			e.restart(t)
			time.Sleep(50 * time.Millisecond)
			if f.calls(path) != 1 {
				t.Fatal("重启后继续了已自关机的计划")
			}
		})
	}
}
