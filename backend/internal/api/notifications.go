package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"ups-warden/internal/audit"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
)

type channelInput struct {
	Name    *string        `json:"name"`
	Type    *string        `json:"type"`
	Config  *notify.Config `json:"config"`
	Enabled *bool          `json:"enabled"`
	notify.Credentials
	ClearSecrets []string `json:"clear_secrets"`
}

func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+notify.ChannelColumns+" FROM channels WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		c, err := notify.ScanChannel(rows, s.opts.Cipher)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, c.View())
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	next := int64(0)
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1]["id"].(int64)
	}
	page(w, items, next)
}
func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	c, err := notify.ReadChannel(r.Context(), s.opts.DB, s.opts.Cipher, id)
	if err != nil {
		s.storageError(w, err, "channel")
		return
	}
	reply(w, 200, c.View())
}
func applyChannel(c *notify.ChannelRecord, in channelInput) error {
	if in.Type != nil && *in.Type != c.Type {
		c.Type = *in.Type
		c.Config = notify.Config{}
		c.Credentials = notify.Credentials{}
	}
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Config != nil {
		c.Config = *in.Config
	}
	if !validText(c.Name, 128, true) {
		return errValidation
	}
	values := map[string]string{"url": in.URL, "signing_key": in.SigningKey, "token": in.Token, "proxy_url": in.ProxyURL}
	seen := map[string]bool{}
	for _, key := range in.ClearSecrets {
		if seen[key] || values[key] != "" || key == "headers" && len(in.Headers) > 0 {
			return errValidation
		}
		seen[key] = true
		switch key {
		case "url":
			c.Credentials.URL = ""
		case "headers":
			c.Credentials.Headers = nil
		case "signing_key":
			c.Credentials.SigningKey = ""
		case "token":
			c.Credentials.Token = ""
		case "proxy_url":
			c.Credentials.ProxyURL = ""
		default:
			return errValidation
		}
	}
	if in.URL != "" {
		c.Credentials.URL = in.URL
	}
	if in.SigningKey != "" {
		c.Credentials.SigningKey = in.SigningKey
	}
	if in.Token != "" {
		c.Credentials.Token = in.Token
	}
	if in.ProxyURL != "" {
		c.Credentials.ProxyURL = in.ProxyURL
	}
	if len(in.Headers) > 0 {
		c.Credentials.Headers = in.Headers
	}
	notify.NormalizeConfig(c.Type, &c.Config)
	if err := notify.ValidateConfig(c.Type, c.Config, c.Credentials); err != nil {
		return errValidation
	}
	if c.Config.BodyTemplate != "" {
		if err := notify.ValidateTemplate("test", c.Config.Format, c.Config.BodyTemplate); err != nil {
			return errValidation
		}
	}
	return nil
}
func channelAudit(c notify.ChannelRecord) map[string]any {
	v := c.View()
	v["secret"] = c.Credentials
	return v
}
func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) { s.saveChannel(w, r, 0) }
func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if ok {
		s.saveChannel(w, r, id)
	}
}
func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request, id int64) {
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	c := notify.ChannelRecord{Enabled: true}
	var before any
	if id != 0 {
		var err error
		c, err = notify.ReadChannel(r.Context(), s.opts.DB, s.opts.Cipher, id)
		if err != nil {
			s.storageError(w, err, "channel")
			return
		}
		before = channelAudit(c)
	}
	if err := applyChannel(&c, in); err != nil {
		s.storageError(w, err, "channel")
		return
	}
	cfg, encrypted, err := notify.EncodeChannel(c, s.opts.Cipher)
	if err != nil {
		s.internal(w, err)
		return
	}
	err = s.opts.Notify.Change(r.Context(), id, func() (int64, error) {
		err := s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
			kind := "channel.update"
			if id == 0 {
				kind = "channel.create"
				result, err := tx.ExecContext(r.Context(), "INSERT INTO channels(name,type,config,secret,enabled) VALUES(?,?,?,?,?)", c.Name, c.Type, cfg, encrypted, c.Enabled)
				if err != nil {
					return err
				}
				c.ID, err = result.LastInsertId()
				if err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(r.Context(), "UPDATE channels SET name=?,type=?,config=?,secret=?,enabled=? WHERE id=?", c.Name, c.Type, cfg, encrypted, c.Enabled, id); err != nil {
					return err
				}
			}
			return logChange(r.Context(), tx, r, kind, strconv.FormatInt(c.ID, 10), before, channelAudit(c))
		})
		return c.ID, err
	})
	if !s.notificationChanged(w, err) {
		return
	}
	code := 200
	if id == 0 {
		code = 201
	}
	reply(w, code, c.View())
}
func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := notify.ReadChannel(r.Context(), s.opts.DB, s.opts.Cipher, id)
	if err != nil {
		s.storageError(w, err, "channel")
		return
	}
	err = s.opts.Notify.Change(r.Context(), id, func() (int64, error) {
		return id, s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
			var referenced int
			if err := tx.QueryRowContext(r.Context(), `SELECT (SELECT count(*) FROM actions,json_each(actions.config,'$.channel_ids') WHERE actions.type='notify' AND json_each.value=?) + (SELECT count(*) FROM plans,json_each(plans.config,'$.notify_channel_ids') WHERE json_each.value=?)`, id, id).Scan(&referenced); err != nil {
				return err
			}
			if referenced > 0 {
				return errConflict
			}
			// 有历史证据时保留外键拒绝语义，管理员可改为停用。
			if _, err := tx.ExecContext(r.Context(), "DELETE FROM channels WHERE id=?", id); err != nil {
				return err
			}
			return logChange(r.Context(), tx, r, "channel.delete", strconv.FormatInt(id, 10), channelAudit(old), nil)
		})
	})
	if s.notificationChanged(w, err) {
		reply(w, 200, map[string]bool{"ok": true})
	}
}
func (s *Server) notificationChanged(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, notify.ErrReload):
		fail(w, 503, "configuration_saved_runtime_error", "配置已保存，但通知渠道恢复失败；请检查配置后重试")
	case errors.Is(err, notify.ErrClosed):
		fail(w, 503, "notification_unavailable", "通知服务已关闭")
	default:
		s.storageError(w, err, "channel")
	}
	return false
}
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	c, err := notify.ReadChannel(r.Context(), s.opts.DB, s.opts.Cipher, id)
	if err != nil {
		s.storageError(w, err, "channel")
		return
	}
	if !c.Enabled {
		fail(w, 409, "channel_disabled", "请先启用渠道再发送测试消息")
		return
	}
	// 审计记录的是请求而非送达；真正结果由同一发送路径记录到 notify_logs。
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		return audit.Write(r.Context(), tx, audit.Record{UserID: current(r).User.ID, IP: remoteIP(r), Kind: "channel.test", Target: strconv.FormatInt(id, 10), Result: "requested"})
	})
	if err != nil {
		s.internal(w, err)
		return
	}
	err = s.opts.Notify.Enqueue(r.Context(), id, notify.Request{DeviceName: "UPS Warden 测试设备", Event: model.Event{Type: "test", Severity: model.SeverityInfo, At: time.Now()}})
	switch {
	case err == nil:
		reply(w, 202, map[string]any{"accepted": true, "channel_id": id})
	case errors.Is(err, notify.ErrQueueFull):
		fail(w, 429, "notification_queue_full", "渠道发送队列已满，请稍后重试")
	case errors.Is(err, notify.ErrUnavailable), errors.Is(err, notify.ErrClosed):
		fail(w, 503, "notification_unavailable", "通知渠道不可用或正在变更")
	default:
		s.internal(w, err)
	}
}
func (s *Server) notifyTemplates(w http.ResponseWriter, r *http.Request) {
	defaults := notify.DefaultTemplates()
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT event_type,format,body FROM notify_templates")
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	overrides := map[string]string{}
	for rows.Next() {
		var event, format, body string
		if err := rows.Scan(&event, &format, &body); err != nil {
			s.internal(w, err)
			return
		}
		overrides[event+":"+format] = body
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	items := []map[string]any{}
	for _, t := range defaults {
		body, custom := overrides[t.EventType+":"+t.Format]
		if !custom {
			body = t.Body
		}
		items = append(items, map[string]any{"event_type": t.EventType, "format": t.Format, "body": body, "customized": custom})
	}
	reply(w, 200, map[string]any{"items": items})
}
func (s *Server) saveNotifyTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		notify.Template
		Reset bool `json:"reset"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, err := notify.DefaultTemplate(in.EventType, in.Format); err != nil {
		fail(w, 422, "validation_failed", "事件类型或模板格式无效")
		return
	}
	if in.Reset && in.Body != "" || !in.Reset && notify.ValidateTemplate(in.EventType, in.Format, in.Body) != nil {
		fail(w, 422, "validation_failed", "模板校验失败")
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := notify.LoadTemplate(r.Context(), s.opts.DB, in.EventType, in.Format)
	if err != nil {
		s.internal(w, err)
		return
	}
	body := in.Body
	if in.Reset {
		body, err = notify.DefaultTemplate(in.EventType, in.Format)
		if err != nil {
			s.internal(w, err)
			return
		}
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		if in.Reset {
			_, err = tx.ExecContext(r.Context(), "DELETE FROM notify_templates WHERE event_type=? AND format=?", in.EventType, in.Format)
		} else {
			_, err = tx.ExecContext(r.Context(), "INSERT INTO notify_templates(event_type,format,body) VALUES(?,?,?) ON CONFLICT(event_type,format) DO UPDATE SET body=excluded.body", in.EventType, in.Format, in.Body)
		}
		if err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "notify_template.update", in.EventType+":"+in.Format, map[string]any{"body": old}, map[string]any{"body": body})
	})
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, map[string]any{"event_type": in.EventType, "format": in.Format, "body": body, "customized": !in.Reset})
}
func (s *Server) notifyLogs(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	where := "WHERE (?=0 OR id<?)"
	args := []any{before, before}
	for _, key := range []string{"channel_id", "event_id"} {
		if text := r.URL.Query().Get(key); text != "" {
			id, err := strconv.ParseInt(text, 10, 64)
			if err != nil || id <= 0 {
				fail(w, 400, "invalid_id", "筛选 ID 无效")
				return
			}
			where += " AND " + key + "=?"
			args = append(args, id)
		}
	}
	if text := r.URL.Query().Get("ok"); text != "" {
		if text != "true" && text != "false" {
			fail(w, 400, "invalid_filter", "ok 仅支持 true/false")
			return
		}
		where += " AND ok=?"
		args = append(args, text == "true")
	}
	for _, key := range []string{"from", "to"} {
		at, err := timeQuery(r, key)
		if err != nil {
			fail(w, 400, "invalid_time_range", "时间格式无效")
			return
		}
		if !at.IsZero() {
			if key == "from" {
				where += " AND at>=?"
			} else {
				where += " AND at<?"
			}
			args = append(args, at.UnixMilli())
		}
	}
	args = append(args, limit+1)
	items, err := s.notificationRows(r.Context(), where+" ORDER BY id DESC LIMIT ?", args...)
	if err != nil {
		s.internal(w, err)
		return
	}
	next := int64(0)
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	page(w, items, next)
}
func (s *Server) notificationRows(ctx context.Context, where string, args ...any) ([]model.NotificationResult, error) {
	rows, err := s.opts.DB.Read().QueryContext(ctx, "SELECT id,channel_id,event_id,at,summary,ok,duration_ms,error,target,attempts,template_fallback FROM notify_logs "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.NotificationResult{}
	for rows.Next() {
		var v model.NotificationResult
		var at int64
		var event sql.NullInt64
		if err := rows.Scan(&v.ID, &v.ChannelID, &event, &at, &v.Summary, &v.OK, &v.DurationMS, &v.Error, &v.Target, &v.Attempts, &v.TemplateFallback); err != nil {
			return nil, err
		}
		v.At = time.UnixMilli(at).UTC()
		if event.Valid {
			id := event.Int64
			v.EventID = &id
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
