package api

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
	"ups-warden/internal/profile"
)

type profileView struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Version  int    `json:"version"`
	Builtin  bool   `json:"builtin"`
	Content  string `json:"content,omitempty"`
}

func viewProfile(p *profile.Profile, content string) profileView {
	return profileView{p.Key, p.Name, p.Protocol, p.Version, profile.IsBuiltinKey(p.Key), content}
}
func (s *Server) loadProfile(ctx context.Context, key string) (*profile.Profile, string, error) {
	var data []byte
	if profile.IsBuiltinKey(key) {
		b, err := profile.BuiltinContent(key)
		if err != nil {
			return nil, "", err
		}
		data = b
	} else {
		var content string
		if err := s.opts.DB.Read().QueryRowContext(ctx, "SELECT content FROM profiles WHERE key=?", key).Scan(&content); err != nil {
			return nil, "", err
		}
		data = []byte(content)
	}
	p, err := profile.Parse(data)
	return p, string(data), err
}
func (s *Server) profiles(w http.ResponseWriter, r *http.Request) {
	builtins, err := profile.Builtins()
	if err != nil {
		s.internal(w, err)
		return
	}
	items := []profileView{}
	for _, p := range builtins {
		items = append(items, viewProfile(p, ""))
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT key,name,protocol,version FROM profiles ORDER BY key")
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p profileView
		if err = rows.Scan(&p.Key, &p.Name, &p.Protocol, &p.Version); err != nil {
			s.internal(w, err)
			return
		}
		if !profile.IsBuiltinKey(p.Key) {
			items = append(items, p)
		}
	}
	if err = rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	reply(w, 200, map[string]any{"items": items})
}
func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	p, c, err := s.loadProfile(r.Context(), r.PathValue("key"))
	if err != nil {
		s.storageError(w, err, "profile")
		return
	}
	reply(w, 200, viewProfile(p, c))
}
func (s *Server) exportProfile(w http.ResponseWriter, r *http.Request) {
	p, c, err := s.loadProfile(r.Context(), r.PathValue("key"))
	if err != nil {
		s.storageError(w, err, "profile")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+p.Key+`.yaml"`)
	if _, err := w.Write([]byte(c)); err != nil {
		s.opts.Logger.Debug("Profile 导出响应写入失败", "error_type", errorType(err))
	}
}
func (s *Server) validateProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := profile.Parse([]byte(in.Content))
	if err != nil {
		profileFailure(w, err)
		return
	}
	reply(w, 200, map[string]any{"valid": true, "profile": viewProfile(p, "")})
}
func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	s.insertProfile(w, r, in.Content)
}
func (s *Server) insertProfile(w http.ResponseWriter, r *http.Request, content string) {
	p, err := profile.Parse([]byte(content))
	if err != nil {
		profileFailure(w, err)
		return
	}
	if profile.IsBuiltinKey(p.Key) {
		fail(w, 409, "builtin_readonly", "内置 Profile 不可覆盖")
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		now := time.Now().UTC().UnixMilli()
		_, err := tx.ExecContext(r.Context(), "INSERT INTO profiles(key,name,protocol,version,content,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", p.Key, p.Name, p.Protocol, p.Version, content, now, now)
		if err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "profile.create", p.Key, nil, viewProfile(p, content))
	})
	if !s.changed(w, err, "profile") {
		return
	}
	reply(w, 201, viewProfile(p, content))
}
func (s *Server) copyProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	_, content, err := s.loadProfile(r.Context(), r.PathValue("key"))
	if err != nil {
		s.storageError(w, err, "profile")
		return
	}
	var node yaml.Node
	if err = yaml.Unmarshal([]byte(content), &node); err != nil {
		s.internal(w, err)
		return
	}
	fields := node.Content[0].Content
	for i := 0; i < len(fields); i += 2 {
		switch fields[i].Value {
		case "key":
			fields[i+1].Value = in.Key
			fields[i+1].Tag = "!!str"
		case "name":
			fields[i+1].Value = in.Name
			fields[i+1].Tag = "!!str"
		case "version":
			fields[i+1].Value = "1"
			fields[i+1].Tag = "!!int"
		}
	}
	data, err := yaml.Marshal(&node)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.insertProfile(w, r, string(data))
}
func (s *Server) profileDevices(ctx context.Context, key string) ([]int64, error) {
	rows, err := s.opts.DB.Read().QueryContext(ctx, "SELECT DISTINCT device_id FROM sources WHERE profile_key=?", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if profile.IsBuiltinKey(key) {
		fail(w, 409, "builtin_readonly", "内置 Profile 只读，请复制后修改")
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, oldContent, err := s.loadProfile(r.Context(), key)
	if err != nil {
		s.storageError(w, err, "profile")
		return
	}
	p, err := profile.Parse([]byte(in.Content))
	if err != nil || p.Key != key || p.Protocol != old.Protocol || p.Version <= old.Version {
		fail(w, 422, "profile_invalid", "key/protocol 不可改变且 version 必须递增，内容须通过校验")
		return
	}
	ids, err := s.profileDevices(r.Context(), key)
	if err != nil {
		s.internal(w, err)
		return
	}
	err = s.change(r, ids, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), "UPDATE profiles SET name=?,version=?,content=?,updated_at=? WHERE key=?", p.Name, p.Version, in.Content, time.Now().UTC().UnixMilli(), key); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), "UPDATE sources SET profile_version=? WHERE profile_key=?", p.Version, key); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "profile.update", key, viewProfile(old, oldContent), viewProfile(p, in.Content))
	})
	if !s.changed(w, err, "profile") {
		return
	}
	reply(w, 200, viewProfile(p, in.Content))
}
func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if profile.IsBuiltinKey(key) {
		fail(w, 409, "builtin_readonly", "内置 Profile 不可删除")
		return
	}
	if !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	p, c, err := s.loadProfile(r.Context(), key)
	if err != nil {
		s.storageError(w, err, "profile")
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(r.Context(), "SELECT count(*) FROM sources WHERE profile_key=?", key).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errConflict
		}
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM profiles WHERE key=?", key); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "profile.delete", key, viewProfile(p, c), nil)
	})
	if !s.changed(w, err, "profile") {
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

// 数字字符串用于协议诊断寄存器键，不参与业务字段查询。
func registerKey(fc uint8, addr uint16) string {
	return strconv.Itoa(int(fc)) + ":" + strconv.Itoa(int(addr))
}

var profileLocation = regexp.MustCompile(`line ([0-9]+): (fields\[[0-9]+\]|schema|key|name|version|protocol|snmp|modbus|ayi9|phases|power_hints|match)`)

// 解析器错误包含用户提供的文本，只摘取受限位置；不返回错误原文。
func profileFailure(w http.ResponseWriter, err error) {
	details := map[string]any{"path": "$"}
	if match := profileLocation.FindStringSubmatch(err.Error()); len(match) == 3 {
		n, _ := strconv.Atoi(match[1])
		details["line"] = n
		details["path"] = match[2]
	}
	reply(w, 422, map[string]any{"code": "profile_invalid", "message": "Profile 解析或语义校验失败", "details": details})
}
