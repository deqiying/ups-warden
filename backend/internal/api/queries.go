package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ups-warden/internal/history"
	"ups-warden/internal/model"
)

func (s *Server) allSnapshots() []model.DeviceState {
	states := s.opts.Runtime.Snapshots()
	for i := range states {
		states[i] = stateView(states[i])
	}
	return states
}

// 仅在 HTTP 边界转 UTC，不改变状态引擎用于持续时间判断的单调读数。
func stateView(state model.DeviceState) model.DeviceState {
	state = model.CloneState(state)
	state.At = state.At.UTC()
	state.LastOKAt = state.LastOKAt.UTC()
	if state.PendingSince != nil {
		v := state.PendingSince.UTC()
		state.PendingSince = &v
	}
	if state.OnBatterySince != nil {
		v := state.OnBatterySince.UTC()
		state.OnBatterySince = &v
	}
	return state
}
func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]any{"items": s.allSnapshots()})
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if _, err := s.readDevice(r.Context(), id); err != nil {
		s.storageError(w, err, "device")
		return
	}
	for _, v := range s.allSnapshots() {
		if v.DeviceID == id {
			reply(w, 200, v)
			return
		}
	}
	fail(w, 404, "snapshot_unavailable", "设备尚无运行快照或已停用")
}
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if _, err := s.readDevice(r.Context(), id); err != nil {
		s.storageError(w, err, "device")
		return
	}
	from, err := timeQuery(r, "from")
	to, e := timeQuery(r, "to")
	if err != nil || e != nil || from.IsZero() || to.IsZero() || !from.Before(to) {
		fail(w, 400, "invalid_time_range", "需要合法的 from/to RFC3339 时间范围")
		return
	}
	max := 1000
	if text := r.URL.Query().Get("max_points"); text != "" {
		max, err = strconv.Atoi(text)
		if err != nil || max < 1 || max > 1000 {
			fail(w, 400, "invalid_limit", "max_points 必须在 1～1000")
			return
		}
	}
	fields := map[string]bool{}
	if text := r.URL.Query().Get("fields"); text != "" {
		for _, key := range strings.Split(text, ",") {
			spec, ok := model.Lookup(key)
			if !ok || !spec.History || (spec.Phased && model.Phase(key) == 0) {
				fail(w, 422, "invalid_history_field", "仅允许可记录历史的统一字段实例")
				return
			}
			fields[key] = true
		}
	}
	resolution := r.URL.Query().Get("resolution")
	if resolution == "" {
		resolution = "auto"
	}
	if resolution != "auto" && resolution != "raw" && resolution != "1m" {
		fail(w, 400, "invalid_resolution", "分辨率仅支持 auto/raw/1m")
		return
	}
	result, err := s.opts.History.Query(r.Context(), history.Query{DeviceID: id, From: from, To: to, Resolution: resolution, MaxPoints: max})
	if err != nil {
		s.internal(w, err)
		return
	}
	points := []map[string]any{}
	for _, p := range result.Points {
		values := map[string]any{}
		for k, v := range p.Fields {
			if len(fields) == 0 || fields[k] {
				values[k] = map[string]any{"min": v.Min, "max": v.Max, "avg": v.Avg, "count": v.Count}
			}
		}
		points = append(points, map[string]any{"at": p.At.UTC(), "fields": values})
	}
	reply(w, 200, map[string]any{"resolution": result.Resolution, "points": points})
}

type eventView struct {
	ID        int64           `json:"id"`
	DeviceID  int64           `json:"device_id"`
	Type      string          `json:"type"`
	Severity  string          `json:"severity"`
	StartedAt time.Time       `json:"started_at"`
	ClearedAt *time.Time      `json:"cleared_at"`
	Active    bool            `json:"active"`
	Summary   json.RawMessage `json:"summary"`
}

func scanEvent(row rowScanner) (eventView, error) {
	var e eventView
	var started int64
	var cleared sql.NullInt64
	var summary string
	err := row.Scan(&e.ID, &e.DeviceID, &e.Type, &e.Severity, &started, &cleared, &e.Active, &summary)
	e.StartedAt = time.UnixMilli(started).UTC()
	if cleared.Valid {
		v := time.UnixMilli(cleared.Int64).UTC()
		e.ClearedAt = &v
	}
	e.Summary = json.RawMessage(summary)
	return e, err
}

const eventColumns = "id,device_id,type,severity,started_at,cleared_at,active,summary"

func (s *Server) eventRows(ctx context.Context, where string, args ...any) ([]eventView, error) {
	rows, err := s.opts.DB.Read().QueryContext(ctx, "SELECT "+eventColumns+" FROM events "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []eventView{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) { s.listEvents(w, r, false) }
func (s *Server) alerts(w http.ResponseWriter, r *http.Request) { s.listEvents(w, r, true) }
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request, active bool) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	where := "WHERE (?=0 OR id<?)"
	args := []any{before, before}
	if active {
		where += " AND active=1"
	}
	if text := r.URL.Query().Get("device_id"); text != "" {
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, "invalid_id", "device_id 无效")
			return
		}
		where += " AND device_id=?"
		args = append(args, id)
	}
	if kind := r.URL.Query().Get("type"); kind != "" {
		if _, ok := model.EventSeverity(kind); !ok {
			fail(w, 422, "invalid_event_type", "事件类型无效")
			return
		}
		where += " AND type=?"
		args = append(args, kind)
	}
	if sev := r.URL.Query().Get("severity"); sev != "" {
		if sev != "info" && sev != "warning" && sev != "critical" {
			fail(w, 422, "invalid_severity", "事件严重性无效")
			return
		}
		where += " AND severity=?"
		args = append(args, sev)
	}
	for _, key := range []string{"from", "to"} {
		at, err := timeQuery(r, key)
		if err != nil {
			fail(w, 400, "invalid_time_range", "时间格式无效")
			return
		}
		if !at.IsZero() {
			if key == "from" {
				where += " AND started_at>=?"
			} else {
				where += " AND started_at<?"
			}
			args = append(args, at.UnixMilli())
		}
	}
	args = append(args, limit+1)
	items, err := s.eventRows(r.Context(), where+" ORDER BY id DESC LIMIT ?", args...)
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
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT id,at,user_id,ip,kind,target,diff,result FROM audit_logs WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, at int64
		var user sql.NullInt64
		var ip, kind, target, diff, result string
		if err = rows.Scan(&id, &at, &user, &ip, &kind, &target, &diff, &result); err != nil {
			s.internal(w, err)
			return
		}
		var uid any
		if user.Valid {
			uid = user.Int64
		}
		items = append(items, map[string]any{"id": id, "at": time.UnixMilli(at).UTC(), "user_id": uid, "ip": ip, "kind": kind, "target": target, "diff": json.RawMessage(diff), "result": result})
	}
	if err = rows.Err(); err != nil {
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
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	ch, done, cancel := s.opts.Bus.SubscribeWithDone()
	defer cancel()
	var err error
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	send := func(kind string, value any) error {
		select {
		case <-done:
			return context.Canceled
		default:
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err = rc.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data); err != nil {
			return err
		}
		return rc.Flush()
	}
	last := map[int64]time.Time{}
	syncAll := func() error {
		items, e := s.eventRows(r.Context(), "WHERE active=1 ORDER BY id DESC")
		if e != nil {
			return e
		}
		states := s.allSnapshots()
		clear(last)
		for _, state := range states {
			last[state.DeviceID] = state.At
		}
		return send("sync", map[string]any{"schema_version": model.SchemaVersion, "devices": states, "alerts": items, "plan_runs": []any{}})
	}
	if err = syncAll(); err != nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case update, ok := <-ch:
			if !ok {
				return
			}
			if update.NotifyFailed != nil {
				if err = send("notify_failed", update.NotifyFailed); err != nil {
					return
				}
				continue
			}
			if update.Resync {
				if err = syncAll(); err != nil {
					return
				}
				continue
			}
			if _, exists := last[update.State.DeviceID]; !exists {
				// 全量同步中不存在的设备已停用或删除，不让旧队列重新引入它。
				continue
			}
			if !update.State.At.Before(last[update.State.DeviceID]) {
				if err = send("device_state", stateView(update.State)); err != nil {
					return
				}
				last[update.State.DeviceID] = update.State.At
			}
			for _, e := range update.Events {
				if err = send("event", e); err != nil {
					return
				}
			}
			if update.AlertsChanged {
				items, err := s.eventRows(r.Context(), "WHERE active=1 AND device_id=? ORDER BY id DESC", update.State.DeviceID)
				if err != nil {
					return
				}
				if err = send("alert", map[string]any{"device_id": update.State.DeviceID, "items": items}); err != nil {
					return
				}
			}
		case <-ticker.C:
			// 长连接必须响应注销/密码变更与绝对过期，不永远持有建立连接时的授权。
			if _, err = s.opts.Auth.Authenticate(r.Context(), current(r).Token); err != nil {
				return
			}
			select {
			case <-done:
				return
			default:
			}
			if err = rc.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return
			}
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err = rc.Flush(); err != nil {
				return
			}
		}
	}
}
