package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/plan"
)

func createPlanAPI(t *testing.T, f *apiFixture, in any) plan.Record {
	t.Helper()
	w := f.req("POST", "/api/v1/plans", in)
	status(t, w, 201)
	var p plan.Record
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func planRunAPI(t *testing.T, f *apiFixture, id int64, cond func(model.PlanRun) bool) model.PlanRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := f.req("GET", fmt.Sprintf("/api/v1/plan-runs/%d", id), nil)
		status(t, w, 200)
		var v model.PlanRun
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if cond(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatal("等待计划运行状态超时", v.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func planEnded(v model.PlanRun) bool { return v.Status != "countdown" && v.Status != "running" }

func TestPlansCRUDRunCancelAndAudit(t *testing.T) {
	f := newAPI(t)
	for _, path := range []string{"/api/v1/plans", "/api/v1/plan-runs"} {
		status(t, f.req("GET", path, nil), 401)
	}
	f.login(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	step := createActionAPI(t, f, actionHTTPInput(target.URL+"/step"))
	stages := fmt.Sprintf(`[{"steps":[{"action_id":%d}]}]`, step)
	for _, body := range []string{
		`{"name":"p"}`,
		`{"name":"","stages":` + stages + `}`,
		`{"name":"p","countdown_s":3601,"stages":` + stages + `}`,
		`{"name":"p","on_restart":"replay","stages":` + stages + `}`,
		`{"name":"p","notify_channel_ids":[7],"stages":` + stages + `}`,
		`{"name":"p","stages":[{"steps":[{"action_id":999}]}]}`,
	} {
		status(t, f.req("POST", "/api/v1/plans", body), 422)
	}
	status(t, f.req("POST", "/api/v1/plans", `{"name":"p","extra":1}`), 400)
	p := createPlanAPI(t, f, `{"name":"夜间关机","stages":`+stages+`}`)
	if p.CountdownS != 60 || p.OnMainsRestored != "cancel_pending" || p.OnRestart != "resume" || p.NotifyChannelIDs == nil || p.Stages[0].StepTimeoutS != 30 {
		t.Fatal("新建计划默认值错误", p)
	}
	path := fmt.Sprintf("/api/v1/plans/%d", p.ID)
	// 部分更新只替换提供的字段。
	status(t, f.req("PUT", path, `{"countdown_s":0}`), 200)
	w := f.req("GET", path, nil)
	status(t, w, 200)
	var got plan.Record
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Name != "夜间关机" || got.CountdownS != 0 || len(got.Stages) != 1 {
		t.Fatal("部分更新覆盖了未提供字段", got, err)
	}
	status(t, f.req("PUT", path, `{"on_mains_restored":"ignore"}`), 422)
	status(t, f.req("PUT", "/api/v1/plans/999", `{"countdown_s":0}`), 404)

	// 手动执行（含 dry-run）都需输入计划名称确认。
	for _, body := range []string{`{"dry_run":true}`, `{"confirm_name":"其他","dry_run":true}`} {
		status(t, f.req("POST", path+"/run", body), 422)
	}
	status(t, f.req("POST", "/api/v1/plans/999/run", `{"confirm_name":"x"}`), 404)
	status(t, f.req("POST", path+"/run", `{"confirm_name":"夜间关机","device_id":1}`), 409) // 设备暂无快照。
	dry := actionRunID(t, f.req("POST", path+"/run", `{"confirm_name":"夜间关机","dry_run":true}`))
	v := planRunAPI(t, f, dry, planEnded)
	if v.Status != "completed" || !v.DryRun || v.PlanName != "夜间关机" || v.Steps[0].Result.ActionStatus != "dry_run" || calls.Load() != 0 {
		t.Fatal("dry-run 运行错误或产生副作用", v, calls.Load())
	}

	// 设备处于停电周期时，真实运行占用该周期；取消需确认，已结束的运行不能再取消。
	sqlExec(t, f.db, `INSERT INTO devices(id,name) VALUES(1,'fixture')`)
	sqlExec(t, f.db, `INSERT INTO outage_cycles(id,device_id,started_at) VALUES(1,1,1)`)
	cycle := int64(1)
	f.s.opts.Runtime = &snapshotRuntime{states: []model.DeviceState{{DeviceID: 1, At: time.Now(), OutageCycleID: &cycle}}}
	status(t, f.req("PUT", path, `{"countdown_s":60}`), 200)
	live := actionRunID(t, f.req("POST", path+"/run", `{"confirm_name":"夜间关机","device_id":1}`))
	if v := planRunAPI(t, f, live, func(model.PlanRun) bool { return true }); v.Status != "countdown" || v.OutageCycleID == nil || *v.OutageCycleID != 1 {
		t.Fatal("真实运行未进入倒计时或未关联停电周期", v)
	}
	status(t, f.req("POST", path+"/run", `{"confirm_name":"夜间关机","dry_run":true}`), 409)
	cancel := fmt.Sprintf("/api/v1/plan-runs/%d/cancel", live)
	status(t, f.req("POST", cancel, `{}`), 422)
	status(t, f.req("POST", cancel, `{"confirm":true}`), 202)
	if v := planRunAPI(t, f, live, planEnded); v.Status != "cancelled" || v.Steps[0].Status != "cancelled" {
		t.Fatal("取消结果错误", v)
	}
	status(t, f.req("POST", cancel, `{"confirm":true}`), 409)
	status(t, f.req("POST", "/api/v1/plan-runs/999/cancel", `{"confirm":true}`), 404)
	w = f.req("POST", path+"/run", `{"confirm_name":"夜间关机","device_id":1}`)
	status(t, w, 409)
	if !strings.Contains(w.Body.String(), "plan_cycle_executed") {
		t.Fatal("同周期重复执行未拒绝", w.Body.String())
	}
	sqlExec(t, f.db, `UPDATE outage_cycles SET ended_at=2 WHERE id=1`)
	w = f.req("POST", path+"/run", `{"confirm_name":"夜间关机","device_id":1,"dry_run":true}`)
	status(t, w, 409)
	if !strings.Contains(w.Body.String(), "plan_cycle_ended") {
		t.Fatal("已结束周期仍可执行", w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("倒计时取消后仍执行步骤")
	}

	w = f.req("GET", fmt.Sprintf("/api/v1/plan-runs?plan_id=%d&limit=1", p.ID), nil)
	status(t, w, 200)
	var runs struct {
		Items []model.PlanRun `json:"items"`
		Next  int64           `json:"next_before"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil || len(runs.Items) != 1 || runs.Items[0].ID != live || runs.Next == 0 {
		t.Fatal("运行记录分页错误", w.Body.String(), err)
	}
	status(t, f.req("GET", "/api/v1/plan-runs?plan_id=x", nil), 400)
	status(t, f.req("GET", "/api/v1/plan-runs/999", nil), 404)

	// 有运行记录的计划保留执行证据，不能删除；未使用的计划删除需确认。
	status(t, f.req("DELETE", path, `{"confirm":true}`), 409)
	unused := createPlanAPI(t, f, `{"name":"unused","stages":`+stages+`}`)
	unusedPath := fmt.Sprintf("/api/v1/plans/%d", unused.ID)
	status(t, f.req("DELETE", unusedPath, `{}`), 422)
	status(t, f.req("DELETE", unusedPath, `{"confirm":true}`), 200)
	status(t, f.req("GET", unusedPath, nil), 404)
	w = f.req("GET", "/api/v1/plans", nil)
	status(t, w, 200)
	if strings.Count(w.Body.String(), `"name":`) != 1 {
		t.Fatal("计划列表错误", w.Body.String())
	}
	audit := f.req("GET", "/api/v1/audit?limit=200", nil).Body.String()
	for _, kind := range []string{"plan.create", "plan.update", "plan.run", "plan_run.cancel", "plan.delete"} {
		if !strings.Contains(audit, `"`+kind+`"`) {
			t.Fatal("缺少审计", kind)
		}
	}
}

func TestPlanActionAndReferences(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	step := createActionAPI(t, f, actionHTTPInput("http://127.0.0.1:1/step"))
	channel := createNotifyChannel(t, f, map[string]any{"name": "plan", "type": "webhook", "url": "http://127.0.0.1:1/hook", "enabled": true})
	disabled := createNotifyChannel(t, f, map[string]any{"name": "off", "type": "webhook", "url": "http://127.0.0.1:1/off", "enabled": false})
	stages := fmt.Sprintf(`[{"steps":[{"action_id":%d}]}]`, step)
	status(t, f.req("POST", "/api/v1/plans", fmt.Sprintf(`{"name":"p","notify_channel_ids":[%d],"stages":%s}`, disabled, stages)), 422)
	p := createPlanAPI(t, f, fmt.Sprintf(`{"name":"p","notify_channel_ids":[%d],"stages":%s}`, channel, stages))
	status(t, f.req("DELETE", fmt.Sprintf("/api/v1/channels/%d", channel), `{"confirm":true}`), 409)

	status(t, f.req("POST", "/api/v1/actions", `{"name":"start","type":"plan","config":{}}`), 422)
	status(t, f.req("POST", "/api/v1/actions", `{"name":"start","type":"plan","config":{"plan_id":999}}`), 422)
	planAction := createActionAPI(t, f, map[string]any{"name": "start", "type": "plan", "config": map[string]any{"plan_id": p.ID}})
	// plan 动作必须经计划名称确认手动执行，且不能作为计划步骤。
	w := f.req("POST", fmt.Sprintf("/api/v1/actions/%d/run", planAction), `{"confirm":true,"dry_run":true}`)
	status(t, w, 422)
	if !strings.Contains(w.Body.String(), "plan_confirmation_required") {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("POST", "/api/v1/plans", fmt.Sprintf(`{"name":"nested","stages":[{"steps":[{"action_id":%d}]}]}`, planAction)), 422)
	status(t, f.req("POST", "/api/v1/rules", fmt.Sprintf(`{"name":"outage","scope":{"type":"all"},"trigger":{"type":"event","events":["mains_lost"]},"actions":[{"action_id":%d}]}`, planAction)), 201)
	status(t, f.req("DELETE", fmt.Sprintf("/api/v1/plans/%d", p.ID), `{"confirm":true}`), 409)
}

func TestPlanSSESyncAndProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		sqlExec(t, f.db, `INSERT INTO plans(id,name,config) VALUES(1,'p','{}')`)
		sqlExec(t, f.db, `INSERT INTO plan_runs(plan_id,dry_run,status,trigger,plan_snapshot,started_at) VALUES(1,0,'running','{}','{"name":"p"}',1)`)
		sqlExec(t, f.db, `INSERT INTO plan_runs(plan_id,dry_run,status,trigger,plan_snapshot,started_at,ended_at) VALUES(1,0,'completed','{}','{"name":"p"}',1,2)`)
		w, cancel, done := startStream(f)
		defer cancel()
		synctest.Wait()
		body := w.body.String()
		if !strings.Contains(body, "event: sync\n") || !strings.Contains(body, `"plan_runs":[{"id":1,`) || strings.Contains(body, `"id":2,`) {
			t.Fatal("sync 未只包含活动运行", body)
		}
		f.s.opts.Bus.Publish(model.DeviceUpdate{PlanProgress: &model.PlanRun{ID: 1, PlanID: 1, Status: "cancelled", Steps: []model.PlanStep{}}})
		synctest.Wait()
		if !strings.Contains(w.body.String(), "event: plan_progress\n") || strings.Contains(w.body.String(), "event: device_state") {
			t.Fatal(w.body.String())
		}
		cancel()
		<-done
	})
}
