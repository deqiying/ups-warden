package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/collector"
	"ups-warden/internal/config"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/profile"
	"ups-warden/internal/rule"
	"ups-warden/internal/sim"
	"ups-warden/internal/state"
)

func TestM6OutageNotificationAndDryRun(t *testing.T) {
	for _, tc := range []struct{ dry, condition bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		dry := tc.dry
		name := "event"
		if tc.condition {
			name = "condition"
		}
		if dry {
			name += "-dry-run"
		}
		lostType, restoredType := model.EventMainsLost, model.EventMainsRestored
		if tc.condition {
			lostType, restoredType = "rule_triggered", "rule_recovered"
		}
		t.Run(name, func(t *testing.T) {
			deliveries := make(chan string, 8)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				deliveries <- string(body)
				w.WriteHeader(204)
			}))
			defer target.Close()
			cfg := config.Config{DataDir: t.TempDir()}
			a, err := open(context.Background(), cfg, testLogger(), "")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := a.Close(); err != nil {
					t.Error(err)
				}
			}()
			pipelineSQL(t, a.db, `INSERT INTO devices(id,name) VALUES(1,'M6 loopback')`)
			channel := notify.ChannelRecord{Name: "loopback", Type: "webhook", Enabled: true, Credentials: notify.Credentials{URL: target.URL}}
			notify.NormalizeConfig(channel.Type, &channel.Config)
			encoded, encrypted, err := notify.EncodeChannel(channel, a.cipher)
			if err != nil {
				t.Fatal(err)
			}
			err = a.notify.Change(t.Context(), 0, func() (int64, error) {
				res, e := a.db.Write().Exec("INSERT INTO channels(name,type,config,secret,enabled) VALUES(?,?,?,?,1)", channel.Name, channel.Type, encoded, encrypted)
				if e != nil {
					return 0, e
				}
				return res.LastInsertId()
			})
			if err != nil {
				t.Fatal(err)
			}
			act := action.Record{Name: "notify", Type: "notify", TimeoutS: 30, Config: action.Config{ChannelIDs: []int64{1}}}
			ac, sec, err := action.Encode(act, a.cipher)
			if err != nil {
				t.Fatal(err)
			}
			pipelineSQL(t, a.db, "INSERT INTO actions(id,name,type,timeout_s,config,secret) VALUES(1,?,?,?,?,?)", act.Name, act.Type, act.TimeoutS, ac, sec)
			r := rule.Record{Name: "outage", Enabled: true, DryRun: dry, Scope: rule.Scope{Type: "all"}, Trigger: rule.Trigger{Type: "event", Events: []string{model.EventMainsLost}}, Actions: []rule.ActionRef{{ActionID: 1}}, RecoveryActions: []rule.ActionRef{{ActionID: 1}}}
			if tc.condition {
				r.Trigger = rule.Trigger{Type: "condition", AnyOf: []rule.Group{{AllOf: []rule.Condition{{Field: "power_state", Op: "eq", Value: json.RawMessage(`"on_battery"`)}}}}}
			}
			scope, trigger, actions, recovery, err := rule.Encode(r)
			if err != nil {
				t.Fatal(err)
			}
			pipelineSQL(t, a.db, "INSERT INTO rules(name,enabled,dry_run,scope,trigger,actions,recovery_actions) VALUES(?,1,?,?,?,?,?)", r.Name, r.DryRun, scope, trigger, actions, recovery)
			base := time.Now().UTC().Truncate(time.Millisecond)
			var offset atomic.Int64
			server, err := sim.Start(sim.Config{ET1K: true, Scenario: sim.Scenario{Steps: []sim.Step{{At: "1s", State: "outage"}, {At: "20s", State: "recovery"}}}, Now: func() time.Time { return base.Add(time.Duration(offset.Load())) }})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			_, addr, _ := server.Addresses()
			host, port := loopbackPort(t, addr)
			p, err := profile.Builtin("et1k-modbus")
			if err != nil {
				t.Fatal(err)
			}
			probe := collector.New(nil, nil, nil, collector.Options{Logger: testLogger()})
			defer probe.Close()
			source := collector.Source{ID: 1, DeviceID: 1, Protocol: p.Protocol, Host: host, Port: port, Profile: p, Timeout: time.Second}
			actor, err := state.NewActor(t.Context(), a.db, 1, state.DefaultConfig(), a.publish, testLogger())
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
			sample(0)
			sample(1)
			confirmed := time.Now()
			u := sample(11)
			if u.State.PowerState != "on_battery" || len(u.Events) != 1 {
				t.Fatal("停电未确认", u.Events)
			}
			if !dry {
				select {
				case body := <-deliveries:
					if !strings.Contains(body, lostType) || time.Since(confirmed) > 2*time.Second {
						t.Fatal("通知内容或两秒送达期限不满足", body)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("AC-05 确认后两秒内未送达假 Webhook")
				}
			}
			waitM6Runs(t, a, 1)
			if err := a.rules.Process(t.Context(), u); err != nil {
				t.Fatal(err)
			} // 同事件重放不能重复执行。
			sample(20)
			if _, err := actor.Advance(t.Context(), base.Add(80*time.Second)); err != nil {
				t.Fatal(err)
			}
			waitM6Runs(t, a, 2)
			if !dry {
				select {
				case body := <-deliveries:
					if !strings.Contains(body, restoredType) {
						t.Fatal(body)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("恢复通知未送达")
				}
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = open(context.Background(), cfg, testLogger(), "")
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err := a.db.Read().QueryRow("SELECT count(*) FROM action_runs").Scan(&count); err != nil || count != 2 {
				t.Fatal("重启重放动作", count, err)
			}
			if dry {
				if err := a.db.Read().QueryRow("SELECT count(*) FROM action_runs WHERE status='dry_run'").Scan(&count); err != nil || count != 2 {
					t.Fatal("缺少dry-run记录", count, err)
				}
				if err := a.db.Read().QueryRow("SELECT count(*) FROM notify_logs").Scan(&count); err != nil || count != 0 {
					t.Fatal("dry-run写入发送记录", count, err)
				}
				select {
				case body := <-deliveries:
					t.Fatal("AC-09 dry-run产生外部副作用", body)
				default:
				}
			}
			var linked string
			if err := a.db.Read().QueryRow("SELECT action_run_ids FROM rule_logs WHERE kind='fired'").Scan(&linked); err != nil {
				t.Fatal(err)
			}
			var ids []int64
			if err := json.Unmarshal([]byte(linked), &ids); err != nil || len(ids) != 1 {
				t.Fatal(linked, err)
			}
		})
	}
}
func waitM6Runs(t *testing.T, a *App, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var n int
		if err := a.db.Read().QueryRowContext(ctx, "SELECT count(*) FROM action_runs WHERE ended_at IS NOT NULL").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("动作未完成", n, want)
		}
	}
}

func TestM6CloseWithFailedRulesAndFullQueue(t *testing.T) {
	a, err := open(context.Background(), config.Config{DataDir: t.TempDir()}, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	pipelineSQL(t, a.db, `INSERT INTO devices(id,name) VALUES(1,'fixture')`)
	pipelineSQL(t, a.db, `INSERT INTO rules(name,scope,trigger,actions) VALUES('fixture','{"type":"all"}','{"type":"condition","any_of":[{"all_of":[{"field":"low_battery","op":"eq","value":true}]}]}','[{"action_id":1}]')`)
	pipelineSQL(t, a.db, `CREATE TRIGGER reject_rules BEFORE INSERT ON rule_logs BEGIN SELECT RAISE(ABORT,'fixture full disk'); END`)
	u := model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, At: time.Now(), Fields: map[string]model.Field{"low_battery": {Bool: true, Quality: model.Good}}}}
	for range 257 {
		if err := a.rules.Enqueue(u); err != nil {
			t.Fatal(err)
		}
	}
	// 模拟持有 actor 生命周期的最后一次发布，队列满时它必须先于 Close 返回。
	publisherDone := make(chan struct{})
	a.stateDone[1] = publisherDone
	go func() { a.publish(u); close(publisherDone) }()
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("数据库持续失败却报告成功关闭")
		}
	case <-time.After(8 * time.Second):
		a.rules.BeginClose()
		<-closed
		t.Fatal("规则满队列与 actor 退出形成死锁")
	}
}
