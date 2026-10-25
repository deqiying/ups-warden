package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"ups-warden/internal/state"
)

type device struct {
	ID       int64        `json:"id"`
	Name     string       `json:"name"`
	Note     string       `json:"note"`
	Location string       `json:"location"`
	Tags     []string     `json:"tags"`
	Enabled  bool         `json:"enabled"`
	Config   state.Config `json:"config"`
}
type deviceInput struct {
	Name     *string         `json:"name"`
	Note     *string         `json:"note"`
	Location *string         `json:"location"`
	Tags     *[]string       `json:"tags"`
	Enabled  *bool           `json:"enabled"`
	Config   json.RawMessage `json:"config"`
}
type rowScanner interface{ Scan(...any) error }

func scanDevice(row rowScanner) (device, error) {
	var d device
	var tags, config string
	err := row.Scan(&d.ID, &d.Name, &d.Note, &d.Location, &tags, &d.Enabled, &config)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(tags), &d.Tags); err != nil {
		return d, err
	}
	d.Config, err = state.ParseConfig([]byte(config))
	return d, err
}

const deviceColumns = "id,name,note,location,tags,enabled,config"

func (s *Server) readDevice(ctx context.Context, id int64) (device, error) {
	return scanDevice(s.opts.DB.Read().QueryRowContext(ctx, "SELECT "+deviceColumns+" FROM devices WHERE id=?", id))
}
func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+deviceColumns+" FROM devices WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, d)
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
func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	d, err := s.readDevice(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "device")
		return
	}
	reply(w, 200, d)
}

// applyDevice 应用显式字段；config 整体替换，省略的阈值字段取 defaults（系统设置）。
func applyDevice(d *device, in deviceInput, defaults state.Config) error {
	if in.Name != nil {
		d.Name = *in.Name
	}
	if in.Note != nil {
		d.Note = *in.Note
	}
	if in.Location != nil {
		d.Location = *in.Location
	}
	if in.Tags != nil {
		d.Tags = *in.Tags
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	if in.Config != nil {
		cfg, err := state.ParseConfigWith(defaults, in.Config)
		if err != nil {
			return errValidation
		}
		d.Config = cfg
	}
	if !validText(d.Name, 128, true) || !validText(d.Note, 4096, false) || !validText(d.Location, 256, false) || len(d.Tags) > 32 {
		return errValidation
	}
	for _, tag := range d.Tags {
		if !validText(tag, 64, true) {
			return errValidation
		}
	}
	return nil
}
func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceInput
	if !decode(w, r, &in) {
		return
	}
	defaults, err := s.deviceDefaults(r)
	if err != nil {
		s.internal(w, err)
		return
	}
	d := device{Enabled: true, Tags: []string{}, Config: defaults}
	if err := applyDevice(&d, in, defaults); err != nil {
		s.storageError(w, err, "device")
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	err = s.change(r, nil, func(tx *sql.Tx) error {
		tags, _ := json.Marshal(d.Tags)
		cfg, _ := json.Marshal(d.Config)
		result, err := tx.ExecContext(r.Context(), "INSERT INTO devices(name,note,location,tags,enabled,config) VALUES(?,?,?,?,?,?)", d.Name, d.Note, d.Location, string(tags), d.Enabled, string(cfg))
		if err != nil {
			return err
		}
		d.ID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "device.create", strconv.FormatInt(d.ID, 10), nil, d)
	})
	if !s.changed(w, err, "device") {
		return
	}
	reply(w, 201, d)
}
func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in deviceInput
	if !decode(w, r, &in) {
		return
	}
	s.saveDevice(w, r, id, in)
}
func (s *Server) enableDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		fail(w, 422, "enabled_required", "需要 enabled")
		return
	}
	s.saveDevice(w, r, id, deviceInput{Enabled: in.Enabled})
}
func (s *Server) saveDevice(w http.ResponseWriter, r *http.Request, id int64, in deviceInput) {
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := s.readDevice(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "device")
		return
	}
	d := old
	defaults, err := s.deviceDefaults(r)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err = applyDevice(&d, in, defaults); err != nil {
		s.storageError(w, err, "device")
		return
	}
	err = s.change(r, []int64{id}, func(tx *sql.Tx) error {
		tags, _ := json.Marshal(d.Tags)
		cfg, _ := json.Marshal(d.Config)
		if _, err := tx.ExecContext(r.Context(), "UPDATE devices SET name=?,note=?,location=?,tags=?,enabled=?,config=? WHERE id=?", d.Name, d.Note, d.Location, string(tags), d.Enabled, string(cfg), id); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "device.update", strconv.FormatInt(id, 10), old, d)
	})
	if !s.changed(w, err, "device") {
		return
	}
	reply(w, 200, d)
}
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := s.readDevice(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "device")
		return
	}
	err = s.change(r, []int64{id}, func(tx *sql.Tx) error {
		// JSON 作用范围没有外键，显式保留规则引用；历史证据仍由外键保护。
		var referenced int
		if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM rules,json_each(rules.scope,'$.device_ids') WHERE json_each.value=?`, id).Scan(&referenced); err != nil {
			return err
		}
		if referenced > 0 {
			return errConflict
		}
		for _, query := range []string{"DELETE FROM events WHERE device_id=?", "DELETE FROM device_state WHERE device_id=?", "DELETE FROM outage_cycles WHERE device_id=?", "DELETE FROM devices WHERE id=?"} {
			if _, err := tx.ExecContext(r.Context(), query, id); err != nil {
				return err
			}
		}
		return logChange(r.Context(), tx, r, "device.delete", strconv.FormatInt(id, 10), old, nil)
	})
	if !s.changed(w, err, "device") {
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
