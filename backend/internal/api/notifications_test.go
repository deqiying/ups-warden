package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/notify"
)

func createNotifyChannel(t *testing.T, f *apiFixture, input any) int64 {
	t.Helper()
	w := f.req("POST", "/api/v1/channels", input)
	status(t, w, 201)
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.ID
}
func awaitNotifyLogs(t *testing.T, f *apiFixture, id int64, n int) []model.NotificationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond * 5)
	defer tick.Stop()
	for {
		items, err := f.s.notificationRows(ctx, "WHERE channel_id=? ORDER BY id", id)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == n {
			return items
		}
		select {
		case <-ctx.Done():
			t.Fatalf("等待通知日志超时: got %d want %d", len(items), n)
		case <-tick.C:
		}
	}
}
func TestChannelCRUDSecretsAndAudit(t *testing.T) {
	f := newAPI(t)
	for _, path := range []string{"/api/v1/channels", "/api/v1/notify-templates", "/api/v1/notify-logs"} {
		status(t, f.req("GET", path, nil), 401)
	}
	f.login(t)
	input := map[string]any{"name": "测试渠道", "type": "webhook", "url": "http://127.0.0.1:1/private-url", "headers": map[string]string{"Authorization": "private-header"}, "signing_key": "private-signing", "enabled": false}
	id := createNotifyChannel(t, f, input)
	path := fmt.Sprintf("/api/v1/channels/%d", id)
	for _, endpoint := range []string{path, "/api/v1/channels", "/api/v1/audit"} {
		w := f.req("GET", endpoint, nil)
		status(t, w, 200)
		for _, secret := range []string{"private-url", "private-header", "private-signing"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("泄密 endpoint=%s", endpoint)
			}
		}
	}
	c, err := notify.ReadChannel(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	var encrypted []byte
	if err := f.db.Read().QueryRow("SELECT config,secret FROM channels WHERE id=?", id).Scan(&raw, &encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "private-") || bytes.Contains(encrypted, []byte("private-")) {
		t.Fatal("凭据未加密")
	}
	status(t, f.req("PUT", path, map[string]any{"name": "renamed", "url": "", "signing_key": "rotated-signing"}), 200)
	updated, err := notify.ReadChannel(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Credentials.URL != c.Credentials.URL || updated.Credentials.SigningKey != "rotated-signing" {
		t.Fatal("保留/轮换语义错误")
	}
	status(t, f.req("PUT", path, map[string]any{"clear_secrets": []string{"headers", "signing_key"}}), 200)
	updated, err = notify.ReadChannel(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Credentials.SigningKey != "" || len(updated.Credentials.Headers) != 0 {
		t.Fatal("清除失败")
	}
	status(t, f.req("POST", path+"/test", `{}`), 409)
	csrf := f.csrf
	f.csrf = "wrong"
	status(t, f.req("PUT", path, `{"enabled":true}`), 403)
	f.csrf = csrf
	for _, body := range []string{`{"clear_secrets":["url"]}`, `{"clear_secrets":["token","token"]}`, `{"token":"bad-cross-type"}`, `{"clear_secrets":["headers"],"headers":{"X-Key":"secret"}}`, `{"config":{"method":"DELETE"}}`, `{"config":{"body_template":"{{unknown}}"}}`} {
		status(t, f.req("PUT", path, body), 422)
	}
	status(t, f.req("PUT", path, `{"config":{"unknown":1}}`), 400)
	status(t, f.req("DELETE", path, `{"confirm":false}`), 422)
	status(t, f.req("DELETE", path, `{"confirm":true}`), 200)
	status(t, f.req("GET", path, nil), 404)
	audit := f.req("GET", "/api/v1/audit", nil).Body.String()
	if strings.Contains(audit, "rotated-signing") || !strings.Contains(audit, "secret") || !strings.Contains(audit, "已修改") {
		t.Fatal("审计未脱敏记录凭据变更")
	}
}
func TestNotificationWebhookAPIAndLogPagination(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	received := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Method != "PUT" || r.Header.Get("X-Test") != "private-header" || r.Header.Get("X-UPS-Warden-Signature") == "" {
			t.Error("请求契约错误")
		}
		received <- string(body)
		w.WriteHeader(204)
	}))
	defer server.Close()
	id := createNotifyChannel(t, f, map[string]any{"name": "webhook", "type": "webhook", "url": server.URL, "config": map[string]any{"method": "PUT", "body_template": `{"kind":"{{event.type}}","name":"{{device.name}}"}`}, "headers": map[string]string{"X-Test": "private-header"}, "signing_key": "private-signing"})
	path := fmt.Sprintf("/api/v1/channels/%d", id)
	for i := 0; i < 2; i++ {
		status(t, f.req("POST", path+"/test", `{}`), 202)
		select {
		case body := <-received:
			if !strings.Contains(body, `"kind":"test"`) {
				t.Fatal(body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("请求未到达")
		}
	}
	items := awaitNotifyLogs(t, f, id, 2)
	for _, v := range items {
		if !v.OK || v.Attempts != 1 || v.EventID != nil || v.Target != "webhook" {
			t.Fatal(v)
		}
	}
	w := f.req("GET", fmt.Sprintf("/api/v1/notify-logs?channel_id=%d&ok=true&limit=1", id), nil)
	status(t, w, 200)
	var page struct {
		Items []model.NotificationResult `json:"items"`
		Next  int64                      `json:"next_before"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Next == 0 {
		t.Fatal(w.Body.String())
	}
	w = f.req("GET", fmt.Sprintf("/api/v1/notify-logs?before=%d", page.Next), nil)
	status(t, w, 200)
	if strings.Contains(w.Body.String(), "private-header") || strings.Contains(w.Body.String(), "private-signing") {
		t.Fatal("日志泄密")
	}
	status(t, f.req("DELETE", path, `{"confirm":true}`), 409)
	status(t, f.req("PUT", path, `{"enabled":false}`), 200)
	for _, query := range []string{"limit=201", "channel_id=-1", "event_id=no", "ok=1", "from=bad"} {
		status(t, f.req("GET", "/api/v1/notify-logs?"+query, nil), 400)
	}
}
func TestNotifyTemplateManagementAndAuditRollback(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	status(t, f.req("GET", "/api/v1/notify-templates", nil), 200)
	status(t, f.req("PUT", "/api/v1/notify-templates", map[string]any{"event_type": "test", "format": "html", "body": "<b>{{device.name}}</b>"}), 200)
	w := f.req("GET", "/api/v1/notify-templates", nil)
	if !strings.Contains(w.Body.String(), `"customized":true`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("PUT", "/api/v1/notify-templates", `{"event_type":"test","format":"html","reset":true}`), 200)
	for _, body := range []string{`{"event_type":"unknown","format":"text","body":"x"}`, `{"event_type":"test","format":"text","body":"{{secret}}"}`, `{"event_type":"test","format":"json","body":"not json"}`, `{"event_type":"test","format":"text","reset":true,"body":"x"}`} {
		status(t, f.req("PUT", "/api/v1/notify-templates", body), 422)
	}
	id := createNotifyChannel(t, f, map[string]any{"name": "rollback", "type": "webhook", "url": "http://127.0.0.1:1/unused", "enabled": false})
	sqlExec(t, f.db, "CREATE TRIGGER reject_notify_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit failure'); END")
	status(t, f.req("PUT", fmt.Sprintf("/api/v1/channels/%d", id), `{"name":"must-rollback"}`), 409)
	c, err := notify.ReadChannel(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || c.Name != "rollback" {
		t.Fatal(c.Name, err)
	}
	status(t, f.req("PUT", "/api/v1/notify-templates", `{"event_type":"test","format":"text","body":"must rollback"}`), 500)
	var count int
	if err := f.db.Read().QueryRow("SELECT count(*) FROM notify_templates").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestNotifyFailedSSEWithoutDevices(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		w, cancel, done := startStream(f)
		defer cancel()
		synctest.Wait()
		f.s.opts.Bus.Publish(model.DeviceUpdate{NotifyFailed: &model.NotificationResult{ID: 3, ChannelID: 2, Error: "HTTP 503", Attempts: 4, At: time.Now().UTC()}})
		synctest.Wait()
		if !strings.Contains(w.body.String(), "event: notify_failed\n") || strings.Contains(w.body.String(), "event: device_state") {
			t.Fatal(w.body.String())
		}
		cancel()
		<-done
	})
}
func TestTelegramAPISecretsAndTargets(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	const token = "123456:private-bot-token"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/bot"+token+"/sendMessage") {
			t.Error("token path错误")
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{}}`)
	}))
	defer server.Close()
	id := createNotifyChannel(t, f, map[string]any{"name": "telegram", "type": "telegram", "token": token, "config": map[string]any{"api_base": server.URL, "chat_ids": []string{"1", "2"}, "format": "html"}})
	path := fmt.Sprintf("/api/v1/channels/%d", id)
	status(t, f.req("POST", path+"/test", `{}`), 202)
	items := awaitNotifyLogs(t, f, id, 2)
	if calls.Load() != 2 || items[0].Target != "1" || items[1].Target != "2" {
		t.Fatal(calls.Load(), items)
	}
	for _, endpoint := range []string{path, "/api/v1/channels", "/api/v1/audit", "/api/v1/notify-logs"} {
		w := f.req("GET", endpoint, nil)
		status(t, w, 200)
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("Token泄露", endpoint)
		}
	}
	status(t, f.req("PUT", path, `{"token":"invalid/token"}`), 422)
	status(t, f.req("PUT", path, `{"type":"webhook","url":"http://127.0.0.1:1/unused","enabled":false}`), 200)
	c, err := notify.ReadChannel(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || c.Credentials.Token != "" {
		t.Fatal(err)
	}
}
