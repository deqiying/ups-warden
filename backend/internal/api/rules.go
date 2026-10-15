package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/rule"
)

func (s *Server) ruleRoutes() {
	s.mux.HandleFunc("GET /api/v1/rules/templates", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]any{"items": rule.Templates()}) })
	s.mux.HandleFunc("GET /api/v1/rules", s.rules)
	s.mux.HandleFunc("POST /api/v1/rules", func(w http.ResponseWriter, r *http.Request) { s.saveRule(w, r, 0) })
	s.mux.HandleFunc("GET /api/v1/rules/{id}", s.getRule)
	s.mux.HandleFunc("PUT /api/v1/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := idParam(w, r); ok {
			s.saveRule(w, r, id)
		}
	})
	s.mux.HandleFunc("DELETE /api/v1/rules/{id}", s.deleteRule)
	s.mux.HandleFunc("GET /api/v1/rule-logs", s.ruleLogs)
}

type ruleInput struct {
	Name            *string           `json:"name"`
	Enabled         *bool             `json:"enabled"`
	DryRun          *bool             `json:"dry_run"`
	Scope           *rule.Scope       `json:"scope"`
	Trigger         *rule.Trigger     `json:"trigger"`
	Actions         *[]rule.ActionRef `json:"actions"`
	RecoveryActions *[]rule.ActionRef `json:"recovery_actions"`
	CooldownS       *int              `json:"cooldown_s"`
}

func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+rule.Columns+" FROM rules WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []rule.Record{}
	for rows.Next() {
		item, e := rule.Scan(rows)
		if e != nil {
			s.internal(w, e)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
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
func (s *Server) getRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	item, err := rule.Read(r.Context(), s.opts.DB, id)
	if err != nil {
		s.storageError(w, err, "rule")
		return
	}
	reply(w, 200, item)
}
func (s *Server) saveRule(w http.ResponseWriter, r *http.Request, id int64) {
	var in ruleInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	item := rule.Record{Enabled: true, DryRun: true, RecoveryActions: []rule.ActionRef{}}
	var before any
	if id != 0 {
		var err error
		item, err = rule.Read(r.Context(), s.opts.DB, id)
		if err != nil {
			s.storageError(w, err, "rule")
			return
		}
		before = item
	}
	if in.Name != nil {
		item.Name = *in.Name
	}
	if in.Enabled != nil {
		item.Enabled = *in.Enabled
	}
	if in.DryRun != nil {
		item.DryRun = *in.DryRun
	}
	if in.Scope != nil {
		item.Scope = *in.Scope
	}
	if in.Trigger != nil {
		item.Trigger = *in.Trigger
	}
	if in.Actions != nil {
		item.Actions = *in.Actions
	}
	if in.RecoveryActions != nil {
		item.RecoveryActions = *in.RecoveryActions
	}
	if in.CooldownS != nil {
		item.CooldownS = *in.CooldownS
	}
	if err := rule.Validate(item); err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	scope, trigger, actions, recovery, err := rule.Encode(item)
	if err != nil {
		s.internal(w, err)
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		for _, deviceID := range item.Scope.DeviceIDs {
			var exists int
			if err := tx.QueryRowContext(r.Context(), "SELECT 1 FROM devices WHERE id=?", deviceID).Scan(&exists); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return errValidation
				}
				return err
			}
		}
		for _, refs := range [][]rule.ActionRef{item.Actions, item.RecoveryActions} {
			for _, ref := range refs {
				var kind string
				if err := tx.QueryRowContext(r.Context(), "SELECT type FROM actions WHERE id=?", ref.ActionID).Scan(&kind); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return errValidation
					}
					return err
				}
				if kind != "notify" && kind != "http" && kind != "script" && kind != "ssh" {
					return errValidation
				}
			}
		}
		kind := "rule.update"
		if id == 0 {
			kind = "rule.create"
			res, err := tx.ExecContext(r.Context(), "INSERT INTO rules(name,enabled,dry_run,scope,trigger,actions,recovery_actions,cooldown_s) VALUES(?,?,?,?,?,?,?,?)", item.Name, item.Enabled, item.DryRun, scope, trigger, actions, recovery, item.CooldownS)
			if err != nil {
				return err
			}
			item.ID, err = res.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(r.Context(), "UPDATE rules SET name=?,enabled=?,dry_run=?,scope=?,trigger=?,actions=?,recovery_actions=?,cooldown_s=? WHERE id=?", item.Name, item.Enabled, item.DryRun, scope, trigger, actions, recovery, item.CooldownS, id); err != nil {
				return err
			}
			old := before.(rule.Record)
			// 名称、动作或 dry-run 修改不重放已消费边沿；只有定义变化才开始新实例。
			if !reflect.DeepEqual(old.Scope, item.Scope) || !reflect.DeepEqual(old.Trigger, item.Trigger) {
				if _, err := tx.ExecContext(r.Context(), "DELETE FROM rule_state WHERE rule_id=?", id); err != nil {
					return err
				}
			} else if old.Enabled != item.Enabled {
				if _, err := tx.ExecContext(r.Context(), `UPDATE rule_state SET since=json_remove(since,'$.timers') WHERE rule_id=?`, id); err != nil {
					return err
				}
			}
		}
		return logChange(r.Context(), tx, r, kind, strconv.FormatInt(item.ID, 10), before, item)
	})
	if err != nil {
		s.storageError(w, err, "rule")
		return
	}
	code := 200
	if id == 0 {
		code = 201
	}
	reply(w, code, struct {
		rule.Record
		Warnings []string `json:"warnings"`
	}{item, rule.Warnings(item)})
}
func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	before, err := rule.Read(r.Context(), s.opts.DB, id)
	if err != nil {
		s.storageError(w, err, "rule")
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM rules WHERE id=?", id); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "rule.delete", strconv.FormatInt(id, 10), before, nil)
	})
	if err != nil {
		s.storageError(w, err, "rule")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Server) ruleLogs(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	filters := map[string]int64{"rule_id": 0, "device_id": 0}
	for key := range filters {
		if raw := r.URL.Query().Get(key); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				fail(w, 400, "invalid_filter", "筛选 ID 必须为正整数")
				return
			}
			filters[key] = id
		}
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), `SELECT id,rule_id,device_id,at,kind,reason,"values",action_run_ids FROM rule_logs WHERE (?=0 OR id<?) AND (?=0 OR rule_id=?) AND (?=0 OR device_id=?) ORDER BY id DESC LIMIT ?`, before, before, filters["rule_id"], filters["rule_id"], filters["device_id"], filters["device_id"], limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type entry struct {
		ID           int64                  `json:"id"`
		RuleID       int64                  `json:"rule_id"`
		DeviceID     int64                  `json:"device_id"`
		At           time.Time              `json:"at"`
		Kind         string                 `json:"kind"`
		Reason       string                 `json:"reason"`
		Values       map[string]model.Field `json:"values"`
		ActionRunIDs []int64                `json:"action_run_ids"`
	}
	items := []entry{}
	for rows.Next() {
		var v entry
		var at int64
		var values, ids []byte
		if err = rows.Scan(&v.ID, &v.RuleID, &v.DeviceID, &at, &v.Kind, &v.Reason, &values, &ids); err != nil {
			s.internal(w, err)
			return
		}
		v.At = time.UnixMilli(at).UTC()
		if err = json.Unmarshal(values, &v.Values); err != nil {
			s.internal(w, err)
			return
		}
		if err = json.Unmarshal(ids, &v.ActionRunIDs); err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
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
