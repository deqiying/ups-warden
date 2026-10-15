package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ups-warden/internal/collector"
	"ups-warden/internal/driver"
	"ups-warden/internal/secret"
)

type source struct {
	ID             int64           `json:"id"`
	DeviceID       int64           `json:"device_id"`
	Protocol       string          `json:"protocol"`
	Host           string          `json:"host"`
	Port           int             `json:"port"`
	Params         json.RawMessage `json:"params"`
	ProfileKey     string          `json:"profile_key"`
	ProfileVersion int             `json:"profile_version"`
	PollMS         int             `json:"poll_ms"`
	TimeoutMS      int             `json:"timeout_ms"`
	Retries        int             `json:"retries"`
	Enabled        bool            `json:"enabled"`
	PasswordSet    bool            `json:"password_set"`
	CommunitySet   bool            `json:"community_set"`
	Encrypted      []byte          `json:"-"`
	Status         *sourceStatus   `json:"status,omitempty"`
}

type sourceStatus struct {
	LastOKAt  time.Time `json:"last_ok_at"`
	LatencyMS int64     `json:"latency_ms"`
	Failures  int       `json:"failures"`
	ErrorCode string    `json:"error_code"`
}
type sourceInput struct {
	Protocol       *string         `json:"protocol"`
	Host           *string         `json:"host"`
	Port           *int            `json:"port"`
	Params         json.RawMessage `json:"params"`
	ProfileKey     *string         `json:"profile_key"`
	ProfileVersion *int            `json:"profile_version"`
	PollMS         *int            `json:"poll_ms"`
	TimeoutMS      *int            `json:"timeout_ms"`
	Retries        *int            `json:"retries"`
	Enabled        *bool           `json:"enabled"`
	Password       string          `json:"password"`
	Community      string          `json:"community"`
	ClearSecrets   []string        `json:"clear_secrets"`
}

const sourceColumns = "id,device_id,protocol,host,port,params,profile_key,profile_version,poll_ms,timeout_ms,retries,enabled,secret"

func scanSource(row rowScanner) (source, error) {
	var v source
	var params string
	err := row.Scan(&v.ID, &v.DeviceID, &v.Protocol, &v.Host, &v.Port, &params, &v.ProfileKey, &v.ProfileVersion, &v.PollMS, &v.TimeoutMS, &v.Retries, &v.Enabled, &v.Encrypted)
	v.Params = json.RawMessage(params)
	v.PasswordSet = len(v.Encrypted) > 0 && v.Protocol == driver.ProtocolAYI9
	v.CommunitySet = len(v.Encrypted) > 0 && v.Protocol == driver.ProtocolSNMP
	return v, err
}
func (s *Server) readSource(ctx context.Context, id int64) (source, error) {
	return scanSource(s.opts.DB.Read().QueryRowContext(ctx, "SELECT "+sourceColumns+" FROM sources WHERE id=?", id))
}
func (s *Server) sources(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if _, err := s.readDevice(r.Context(), id); err != nil {
		s.storageError(w, err, "device")
		return
	}
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+sourceColumns+" FROM sources WHERE device_id=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?", id, before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []source{}
	for rows.Next() {
		v, err := scanSource(rows)
		if err != nil {
			s.internal(w, err)
			return
		}
		if status, ok := s.opts.Collector.Status(v.ID); ok {
			v.Status = &sourceStatus{status.LastOKAt.UTC(), status.Latency.Milliseconds(), status.Failures, status.ErrorCode}
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
func (s *Server) applySource(ctx context.Context, v *source, in sourceInput) error {
	oldProtocol := v.Protocol
	if in.Protocol != nil {
		v.Protocol = *in.Protocol
	}
	if in.Host != nil {
		v.Host = *in.Host
	}
	if in.Port != nil {
		v.Port = *in.Port
	}
	if in.Params != nil {
		v.Params = in.Params
	}
	if in.ProfileKey != nil {
		v.ProfileKey = *in.ProfileKey
	}
	if in.ProfileVersion != nil {
		v.ProfileVersion = *in.ProfileVersion
	}
	if in.PollMS != nil {
		v.PollMS = *in.PollMS
	}
	if in.TimeoutMS != nil {
		v.TimeoutMS = *in.TimeoutMS
	}
	if in.Retries != nil {
		v.Retries = *in.Retries
	}
	if in.Enabled != nil {
		v.Enabled = *in.Enabled
	}
	if oldProtocol != "" && oldProtocol != v.Protocol {
		v.Encrypted = nil
	}
	if !validHost(v.Host) || v.Port < 1 || v.Port > 65535 || collector.ValidateTiming(int64(v.PollMS), int64(v.TimeoutMS), v.Retries) != nil {
		return errValidation
	}
	if in.Password != "" && v.Protocol != driver.ProtocolAYI9 || in.Community != "" && v.Protocol != driver.ProtocolSNMP {
		return errValidation
	}
	for _, key := range in.ClearSecrets {
		if (key != "password" || v.Protocol != driver.ProtocolAYI9) && (key != "community" || v.Protocol != driver.ProtocolSNMP) {
			return errValidation
		}
		v.Encrypted = nil
	}
	value := in.Password
	if v.Protocol == driver.ProtocolSNMP {
		value = in.Community
	}
	if value != "" {
		if len(in.ClearSecrets) > 0 {
			return errValidation
		}
		encrypted, err := s.opts.Cipher.Encrypt([]byte(value), "sources.secret")
		if err != nil {
			return err
		}
		v.Encrypted = encrypted
	}
	v.PasswordSet = len(v.Encrypted) > 0 && v.Protocol == driver.ProtocolAYI9
	v.CommunitySet = len(v.Encrypted) > 0 && v.Protocol == driver.ProtocolSNMP
	_, err := s.sourceConfig(ctx, *v)
	return err
}
func validHost(host string) bool {
	if len(host) < 1 || len(host) > 253 || strings.ContainsAny(host, "/\\@%?# \t\r\n") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsUnspecified() && !ip.IsMulticast()
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (s *Server) sourceConfig(ctx context.Context, v source) (collector.Source, error) {
	p, _, err := s.loadProfile(ctx, v.ProfileKey)
	if err != nil {
		return collector.Source{}, err
	}
	if p.Version != v.ProfileVersion || p.Protocol != v.Protocol {
		return collector.Source{}, errValidation
	}
	params, err := collector.ParseParams(string(v.Params), v.Protocol)
	if err != nil {
		return collector.Source{}, errValidation
	}
	plain := []byte(nil)
	if len(v.Encrypted) > 0 {
		plain, err = s.opts.Cipher.Decrypt(v.Encrypted, "sources.secret")
		if err != nil {
			return collector.Source{}, err
		}
		defer clear(plain)
	}
	cfg := collector.Source{ID: v.ID, DeviceID: v.DeviceID, Protocol: v.Protocol, Host: v.Host, Port: v.Port, Profile: p, PollInterval: time.Duration(v.PollMS) * time.Millisecond, Timeout: time.Duration(v.TimeoutMS) * time.Millisecond, Retries: v.Retries, Params: params, Credential: secret.NewString(string(plain))}
	if err = collector.ValidateSource(cfg); err != nil {
		return cfg, errValidation
	}
	return cfg, nil
}
func defaultSource(id int64) source {
	return source{DeviceID: id, Params: json.RawMessage(`{}`), ProfileVersion: 1, PollMS: 3000, TimeoutMS: 1000, Enabled: true}
}
func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in sourceInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	if _, err := s.readDevice(r.Context(), id); err != nil {
		s.storageError(w, err, "device")
		return
	}
	v := defaultSource(id)
	if err := s.applySource(r.Context(), &v, in); err != nil {
		s.storageError(w, err, "source")
		return
	}
	err := s.change(r, []int64{id}, func(tx *sql.Tx) error {
		if err := singleSource(r.Context(), tx, v); err != nil {
			return err
		}
		result, err := tx.ExecContext(r.Context(), "INSERT INTO sources(device_id,protocol,host,port,params,profile_key,profile_version,poll_ms,timeout_ms,retries,enabled,secret) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", id, v.Protocol, v.Host, v.Port, string(v.Params), v.ProfileKey, v.ProfileVersion, v.PollMS, v.TimeoutMS, v.Retries, v.Enabled, v.Encrypted)
		if err != nil {
			return err
		}
		v.ID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "source.create", strconv.FormatInt(v.ID, 10), nil, v)
	})
	if !s.changed(w, err, "source") {
		return
	}
	reply(w, 201, v)
}
func singleSource(ctx context.Context, tx *sql.Tx, v source) error {
	if !v.Enabled {
		return nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sources WHERE device_id=? AND enabled=1 AND id<>?", v.DeviceID, v.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errConflict
	}
	return nil
}
func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in sourceInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := s.readSource(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "source")
		return
	}
	v := old
	if err = s.applySource(r.Context(), &v, in); err != nil {
		s.storageError(w, err, "source")
		return
	}
	err = s.change(r, []int64{v.DeviceID}, func(tx *sql.Tx) error {
		if err := singleSource(r.Context(), tx, v); err != nil {
			return err
		}
		_, err := tx.ExecContext(r.Context(), "UPDATE sources SET protocol=?,host=?,port=?,params=?,profile_key=?,profile_version=?,poll_ms=?,timeout_ms=?,retries=?,enabled=?,secret=? WHERE id=?", v.Protocol, v.Host, v.Port, string(v.Params), v.ProfileKey, v.ProfileVersion, v.PollMS, v.TimeoutMS, v.Retries, v.Enabled, v.Encrypted, id)
		if err != nil {
			return err
		}
		before, err := object(old)
		if err != nil {
			return err
		}
		after, err := object(v)
		if err != nil {
			return err
		}
		if in.Password != "" || in.Community != "" || len(in.ClearSecrets) > 0 {
			before["secret"] = "before"
			after["secret"] = "changed"
		}
		return logChange(r.Context(), tx, r, "source.update", strconv.FormatInt(id, 10), before, after)
	})
	if !s.changed(w, err, "source") {
		return
	}
	reply(w, 200, v)
}
func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := s.readSource(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "source")
		return
	}
	err = s.change(r, []int64{old.DeviceID}, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM sources WHERE id=?", id); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "source.delete", strconv.FormatInt(id, 10), old, nil)
	})
	if !s.changed(w, err, "source") {
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
