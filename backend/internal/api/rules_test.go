package api

import (
	"encoding/json"
	"strings"
	"testing"

	"ups-warden/internal/rule"
)

func TestRulesCRUDValidationReferencesAndAudit(t *testing.T) {
	f := newAPI(t)
	status(t, f.req("GET", "/api/v1/rules", nil), 401)
	f.login(t)
	sqlExec(t, f.db, `INSERT INTO devices(id,name) VALUES(1,'fixture')`)
	sqlExec(t, f.db, `INSERT INTO actions(id,name,type,config,timeout_s) VALUES(1,'fixture','notify','{"channel_ids":[1]}',30)`)
	body := `{"name":"outage","scope":{"type":"device","device_ids":[1]},"trigger":{"type":"event","events":["mains_lost"]},"actions":[{"action_id":1}]}`
	w := f.req("POST", "/api/v1/rules", body)
	status(t, w, 201)
	var r rule.Record
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if !r.DryRun || !r.Enabled || r.ID != 1 || r.RecoveryActions == nil {
		t.Fatal(r)
	}
	for _, route := range []string{"/api/v1/rules", "/api/v1/rules/1", "/api/v1/rules/templates", "/api/v1/rule-logs?rule_id=1&device_id=1"} {
		status(t, f.req("GET", route, nil), 200)
	}
	status(t, f.req("GET", "/api/v1/rule-logs?rule_id=no", nil), 400)
	status(t, f.req("GET", "/api/v1/rule-logs?limit=201", nil), 400)
	status(t, f.req("DELETE", "/api/v1/devices/1", `{"confirm":true}`), 409)
	status(t, f.req("DELETE", "/api/v1/actions/1", `{"confirm":true}`), 409)
	for _, invalid := range []string{
		strings.Replace(body, `"action_id":1`, `"action_id":99`, 1),
		strings.Replace(body, `"device_ids":[1]`, `"device_ids":[99]`, 1),
		strings.Replace(body, `"mains_lost"`, `"unknown_event"`, 1),
		strings.Replace(body, `"type":"device"`, `"type":"other"`, 1),
	} {
		status(t, f.req("POST", "/api/v1/rules", invalid), 422)
	}
	status(t, f.req("PUT", "/api/v1/rules/1", `{"trigger":{"type":"condition","any_of":[{"all_of":[{"field":"low_battery","op":"eq","value":true,"unknown":1}]}]}}`), 400)
	sqlExec(t, f.db, `INSERT INTO rule_state(rule_id,device_id,fired,since) VALUES(1,1,1,'{"activated":true}')`)
	status(t, f.req("PUT", "/api/v1/rules/1", `{"name":"renamed","dry_run":false}`), 200)
	var fired bool
	if err := f.db.Read().QueryRow("SELECT fired FROM rule_state WHERE rule_id=1").Scan(&fired); err != nil || !fired {
		t.Fatal("重命名或关闭演练重置已消费边沿", err)
	}
	status(t, f.req("PUT", "/api/v1/rules/1", `{"enabled":false}`), 200)
	status(t, f.req("PUT", "/api/v1/rules/1", `{"enabled":true}`), 200)
	if err := f.db.Read().QueryRow("SELECT fired FROM rule_state WHERE rule_id=1").Scan(&fired); err != nil || !fired {
		t.Fatal("重新启用重置边沿", err)
	}
	w = f.req("PUT", "/api/v1/rules/1", `{"trigger":{"type":"condition","any_of":[{"all_of":[{"field":"runtime_estimate","op":"lte","value":300}]}]}}`)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), "不应作为关机动作的唯一依据") {
		t.Fatal(w.Body.String())
	}
	var count int
	if err := f.db.Read().QueryRow("SELECT count(*) FROM rule_state").Scan(&count); err != nil || count != 0 {
		t.Fatal("条件定义变化未重置实例", count, err)
	}
	sqlExec(t, f.db, `CREATE TRIGGER reject_rule_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	status(t, f.req("PUT", "/api/v1/rules/1", `{"name":"must-rollback"}`), 409)
	current, err := rule.Read(t.Context(), f.db, 1)
	if err != nil || current.Name != "renamed" {
		t.Fatal(current, err)
	}
	sqlExec(t, f.db, "DROP TRIGGER reject_rule_audit")
	status(t, f.req("DELETE", "/api/v1/rules/1", `{"confirm":false}`), 422)
	status(t, f.req("DELETE", "/api/v1/rules/1", `{"confirm":true}`), 200)
	status(t, f.req("GET", "/api/v1/rules/1", nil), 404)
}
func TestRuleLogsPaginationAndHistoryProtection(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	sqlExec(t, f.db, `INSERT INTO devices(id,name) VALUES(1,'fixture')`)
	sqlExec(t, f.db, `INSERT INTO actions(id,name,type,config,timeout_s) VALUES(1,'fixture','notify','{"channel_ids":[1]}',30)`)
	status(t, f.req("POST", "/api/v1/rules", `{"name":"fixture","scope":{"type":"all"},"trigger":{"type":"event","events":["mains_lost"]},"actions":[{"action_id":1}]}`), 201)
	for range 2 {
		sqlExec(t, f.db, `INSERT INTO rule_logs(rule_id,device_id,at,kind,reason,"values",action_run_ids) VALUES(1,1,1,'fired','mains_lost','{"low_battery":{"quality":"good","bool":true}}','[1]')`)
	}
	w := f.req("GET", "/api/v1/rule-logs?limit=1", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"next_before":2`) || !strings.Contains(w.Body.String(), `"action_run_ids":[1]`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("DELETE", "/api/v1/rules/1", `{"confirm":true}`), 409)
	status(t, f.req("PUT", "/api/v1/rules/1", `{"enabled":false}`), 200)
}
func TestNotificationActionReferencePreventsChannelDeletion(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	status(t, f.req("POST", "/api/v1/channels", `{"name":"unused","type":"webhook","enabled":false,"url":"http://127.0.0.1:1/hook"}`), 201)
	sqlExec(t, f.db, `INSERT INTO actions(name,type,config,timeout_s) VALUES('fixture','notify','{"channel_ids":[1]}',30)`)
	status(t, f.req("DELETE", "/api/v1/channels/1", `{"confirm":true}`), 409)
}
