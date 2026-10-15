package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/audit"
	"ups-warden/internal/model"
	"ups-warden/internal/secret"
)

func (s *Server) actionRoutes() {
	s.mux.HandleFunc("GET /api/v1/actions", s.actions)
	s.mux.HandleFunc("POST /api/v1/actions", s.createAction)
	s.mux.HandleFunc("GET /api/v1/actions/{id}", s.getAction)
	s.mux.HandleFunc("PUT /api/v1/actions/{id}", s.updateAction)
	s.mux.HandleFunc("DELETE /api/v1/actions/{id}", s.deleteAction)
	s.mux.HandleFunc("POST /api/v1/actions/{id}/run", s.runAction)
	s.mux.HandleFunc("GET /api/v1/scripts", s.scripts)
	s.mux.HandleFunc("GET /api/v1/action-runs", s.actionRuns)
}

type actionInput struct {
	Name         *string           `json:"name"`
	Type         *string           `json:"type"`
	TimeoutS     *int              `json:"timeout_s"`
	Config       *action.Config    `json:"config"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	BodyTemplate string            `json:"body_template"`
	ClearSecrets []string          `json:"clear_secrets"`
}

func applyAction(c *action.Record, in actionInput) error {
	if in.Type != nil && *in.Type != c.Type {
		c.Type = *in.Type
		c.Config = action.Config{}
		c.Credentials = action.Credentials{}
	}
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.TimeoutS != nil {
		if *in.TimeoutS < 1 {
			return errValidation
		}
		c.TimeoutS = *in.TimeoutS
	}
	if in.Config != nil {
		c.Config = *in.Config
	}
	seen := map[string]bool{}
	for _, key := range in.ClearSecrets {
		if seen[key] {
			return errValidation
		}
		seen[key] = true
		switch key {
		case "url":
			if in.URL != "" {
				return errValidation
			}
			c.Credentials.URL = secret.String{}
		case "headers":
			if len(in.Headers) > 0 {
				return errValidation
			}
			c.Credentials.Headers = nil
		case "body_template":
			if in.BodyTemplate != "" {
				return errValidation
			}
			c.Credentials.BodyTemplate = secret.String{}
		default:
			return errValidation
		}
	}
	if in.URL != "" {
		c.Credentials.URL = secret.NewString(in.URL)
	}
	if len(in.Headers) > 0 {
		c.Credentials.Headers = make(map[string]secret.String, len(in.Headers))
		for k, v := range in.Headers {
			c.Credentials.Headers[k] = secret.NewString(v)
		}
	}
	if in.BodyTemplate != "" {
		c.Credentials.BodyTemplate = secret.NewString(in.BodyTemplate)
	}
	action.Normalize(c)
	return nil
}
func actionAudit(c action.Record) map[string]any {
	v := c.View()
	headers := make(map[string]string, len(c.Credentials.Headers))
	for k, v := range c.Credentials.Headers {
		headers[k] = v.Reveal()
	}
	// audit.Write 对整个 secret 字段掩码，同时仍能识别同名凭据的轮换。
	v["secret"] = map[string]any{"url": c.Credentials.URL.Reveal(), "headers": headers, "body_template": c.Credentials.BodyTemplate.Reveal()}
	return v
}
func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+action.Columns+" FROM actions WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		c, err := action.Scan(rows, s.opts.Cipher)
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
	var next int64
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1]["id"].(int64)
	}
	page(w, items, next)
}
func (s *Server) getAction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	c, err := action.Read(r.Context(), s.opts.DB, s.opts.Cipher, id)
	if err != nil {
		s.storageError(w, err, "action")
		return
	}
	reply(w, 200, c.View())
}
func (s *Server) createAction(w http.ResponseWriter, r *http.Request) { s.saveAction(w, r, 0) }
func (s *Server) updateAction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if ok {
		s.saveAction(w, r, id)
	}
}
func (s *Server) saveAction(w http.ResponseWriter, r *http.Request, id int64) {
	var in actionInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	c := action.Record{}
	var before any
	if id != 0 {
		var err error
		c, err = action.Read(r.Context(), s.opts.DB, s.opts.Cipher, id)
		if err != nil {
			s.storageError(w, err, "action")
			return
		}
		before = actionAudit(c)
	}
	if err := applyAction(&c, in); err != nil {
		s.storageError(w, err, "action")
		return
	}
	err := s.opts.Actions.Change(r.Context(), id, func() error {
		if err := s.opts.Actions.Validate(r.Context(), c); err != nil {
			return errValidation
		}
		cfg, encrypted, err := action.Encode(c, s.opts.Cipher)
		if err != nil {
			return err
		}
		return s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
			kind := "action.update"
			if id == 0 {
				kind = "action.create"
				result, err := tx.ExecContext(r.Context(), "INSERT INTO actions(name,type,config,secret,timeout_s) VALUES(?,?,?,?,?)", c.Name, c.Type, cfg, encrypted, c.TimeoutS)
				if err != nil {
					return err
				}
				c.ID, err = result.LastInsertId()
				if err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(r.Context(), "UPDATE actions SET name=?,type=?,config=?,secret=?,timeout_s=? WHERE id=?", c.Name, c.Type, cfg, encrypted, c.TimeoutS, id); err != nil {
					return err
				}
			}
			return logChange(r.Context(), tx, r, kind, strconv.FormatInt(c.ID, 10), before, actionAudit(c))
		})
	})
	if !s.actionChanged(w, err) {
		return
	}
	code := 200
	if id == 0 {
		code = 201
	}
	reply(w, code, c.View())
}
func (s *Server) actionChanged(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, action.ErrBusy):
		fail(w, 409, "action_busy", "动作正在执行，不能修改或删除")
	case errors.Is(err, action.ErrClosed):
		fail(w, 503, "action_unavailable", "动作服务已关闭")
	default:
		s.storageError(w, err, "action")
	}
	return false
}
func (s *Server) deleteAction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := action.Read(r.Context(), s.opts.DB, s.opts.Cipher, id)
	if err != nil {
		s.storageError(w, err, "action")
		return
	}
	err = s.opts.Actions.Change(r.Context(), id, func() error {
		return s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
			// JSON 引用与普通外键同样保留；不能删除配置来抹去历史证据。
			var referenced bool
			err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(
   SELECT 1 FROM rules,json_each(rules.actions) j WHERE json_extract(j.value,'$.action_id')=?
   UNION ALL SELECT 1 FROM rules,json_each(rules.recovery_actions) j WHERE json_extract(j.value,'$.action_id')=?
   UNION ALL SELECT 1 FROM plans,json_tree(plans.config) j WHERE j.key='action_id' AND j.atom=?
  )`, id, id, id).Scan(&referenced)
			if err != nil {
				return err
			}
			if referenced {
				return errConflict
			}
			if _, err := tx.ExecContext(r.Context(), "DELETE FROM actions WHERE id=?", id); err != nil {
				return err
			}
			return logChange(r.Context(), tx, r, "action.delete", strconv.FormatInt(id, 10), actionAudit(old), nil)
		})
	})
	if s.actionChanged(w, err) {
		reply(w, 200, map[string]bool{"ok": true})
	}
}
func (s *Server) runAction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Confirm  bool  `json:"confirm"`
		DryRun   bool  `json:"dry_run"`
		DeviceID int64 `json:"device_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !in.Confirm {
		fail(w, 422, "confirmation_required", "需要 confirm=true，包括 dry-run")
		return
	}
	if in.DeviceID < 0 {
		fail(w, 422, "validation_failed", "设备 ID 无效")
		return
	}
	req := action.Request{ActionID: id, DryRun: in.DryRun, Trigger: action.Trigger{Type: "manual", UserID: current(r).User.ID}, Event: model.Event{Type: "test", Severity: model.SeverityInfo, At: time.Now()}}
	if in.DeviceID > 0 {
		found := false
		for _, state := range s.opts.Runtime.Snapshots() {
			if state.DeviceID == in.DeviceID {
				req.State = state
				found = true
				break
			}
		}
		if !found {
			fail(w, 409, "snapshot_unavailable", "设备暂无运行快照")
			return
		}
		if err := s.opts.DB.Read().QueryRowContext(r.Context(), "SELECT name FROM devices WHERE id=?", in.DeviceID).Scan(&req.DeviceName); err != nil {
			s.storageError(w, err, "device")
			return
		}
		req.Event.DeviceID = in.DeviceID
	}
	runID, err := s.opts.Actions.SubmitWithAudit(r.Context(), req, func(tx *sql.Tx, runID int64) error {
		return audit.Write(r.Context(), tx, audit.Record{UserID: current(r).User.ID, IP: remoteIP(r), Kind: "action.run", Target: strconv.FormatInt(id, 10), Result: "accepted", After: map[string]any{"run_id": runID, "dry_run": in.DryRun, "device_id": in.DeviceID}})
	})
	if err != nil {
		s.actionChanged(w, err)
		return
	}
	reply(w, 202, map[string]any{"accepted": true, "run_id": runID})
}
func (s *Server) scripts(w http.ResponseWriter, r *http.Request) {
	items, err := action.Scripts(s.opts.Actions.ScriptDir())
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, map[string]any{"items": items})
}

type actionRunView struct {
	ID         int64          `json:"id"`
	ActionID   int64          `json:"action_id"`
	Trigger    action.Trigger `json:"trigger"`
	DryRun     bool           `json:"dry_run"`
	Status     string         `json:"status"`
	StartedAt  time.Time      `json:"started_at"`
	EndedAt    *time.Time     `json:"ended_at"`
	ExitCode   *int           `json:"exit_code"`
	Stdout     string         `json:"stdout"`
	Stderr     string         `json:"stderr"`
	Error      string         `json:"error"`
	DurationMS int64          `json:"duration_ms"`
}

func (s *Server) actionRuns(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	var actionID int64
	if raw := r.URL.Query().Get("action_id"); raw != "" {
		var err error
		actionID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || actionID <= 0 {
			fail(w, 400, "invalid_filter", "action_id 无效")
			return
		}
	}
	items, err := s.actionRunRows(r.Context(), before, actionID, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	var next int64
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	page(w, items, next)
}
func (s *Server) actionRunRows(ctx context.Context, before, actionID int64, limit int) ([]actionRunView, error) {
	rows, err := s.opts.DB.Read().QueryContext(ctx, "SELECT id,action_id,trigger,dry_run,status,started_at,ended_at,exit_code,stdout,stderr,error FROM action_runs WHERE (?=0 OR id<?) AND (?=0 OR action_id=?) ORDER BY id DESC LIMIT ?", before, before, actionID, actionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []actionRunView{}
	for rows.Next() {
		var v actionRunView
		var trigger string
		var started int64
		var ended sql.NullInt64
		if err := rows.Scan(&v.ID, &v.ActionID, &trigger, &v.DryRun, &v.Status, &started, &ended, &v.ExitCode, &v.Stdout, &v.Stderr, &v.Error); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(trigger), &v.Trigger); err != nil {
			return nil, err
		}
		v.StartedAt = time.UnixMilli(started).UTC()
		if ended.Valid {
			at := time.UnixMilli(ended.Int64).UTC()
			v.EndedAt = &at
			v.DurationMS = ended.Int64 - started
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
