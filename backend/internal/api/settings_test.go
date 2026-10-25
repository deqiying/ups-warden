package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSettingsAPI(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	w := f.req("GET", "/api/v1/settings", nil)
	status(t, w, 200)
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["detail_retention_days"] != float64(7) || got["timezone"] != "UTC" || got["http_action_guard_ups_hosts"] != true {
		t.Fatalf("默认设置错误: %v", got)
	}
	for _, body := range []string{`{"detail_retention_days":0}`, `{"detail_retention_days":30,"aggregate_retention_days":10}`, `{"timezone":"Mars/Base"}`, `{"timezone":"Local"}`, `{"language":"fr"}`, `{"device_defaults":{"battery_low_pct":120}}`} {
		status(t, f.req("PATCH", "/api/v1/settings", body), 422)
	}
	w = f.req("PATCH", "/api/v1/settings", `{"http_action_guard_ups_hosts":false}`)
	status(t, w, 422)
	if !strings.Contains(w.Body.String(), `"confirmation_required"`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("PATCH", "/api/v1/settings", `{"http_action_guard_ups_hosts":false,"confirm_disable_guard":true,"detail_retention_days":1,"timezone":"Asia/Shanghai","device_defaults":{"battery_low_pct":40}}`), 200)
	var guard string
	if err := f.db.Read().QueryRow("SELECT value FROM settings WHERE key='http_action_guard_ups_hosts'").Scan(&guard); err != nil || guard != "false" {
		t.Fatal("护栏设置未保存", guard, err)
	}
	var audits int
	if err := f.db.Read().QueryRow("SELECT count(*) FROM audit_logs WHERE kind='settings.update'").Scan(&audits); err != nil || audits != 1 {
		t.Fatal("设置修改未审计", audits, err)
	}
	// 重新开启护栏不需要确认。
	status(t, f.req("PATCH", "/api/v1/settings", `{"http_action_guard_ups_hosts":true}`), 200)

	// 新设备与省略字段采用系统默认阈值。
	w = f.req("POST", "/api/v1/devices", `{"name":"a"}`)
	status(t, w, 201)
	var d device
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.Config.BatteryLow != 40 {
		t.Fatal("新设备未使用默认阈值", d.Config, err)
	}
	w = f.req("POST", "/api/v1/devices", `{"name":"b","config":{"overload_pct":80}}`)
	status(t, w, 201)
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.Config.BatteryLow != 40 || d.Config.Overload != 80 {
		t.Fatal("省略字段未取系统默认值", d.Config, err)
	}

	// 保留期立即作用于清理：明细保留 1 天，两天前的样本被删除。
	now := time.Now()
	sqlExec(t, f.db, "INSERT INTO history_sample(device_id,ts,data) VALUES(1,?,'{}')", now.Add(-48*time.Hour).UnixMilli())
	if err := f.s.opts.History.Cleanup(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	var samples int
	if err := f.db.Read().QueryRow("SELECT count(*) FROM history_sample").Scan(&samples); err != nil || samples != 0 {
		t.Fatal("保留期设置未生效", samples, err)
	}
}
