package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"ups-warden/internal/audit"
	"ups-warden/internal/host"
	"ups-warden/internal/secret"
)

const hostKeyTTL = 5 * time.Minute

type fetchedHostKey struct {
	address     string
	port        int
	fingerprint string
	expires     time.Time
}

// 指纹候选仅属于当前 Server 的内存，不允许重启后或跨地址复用。
// pending 与 hosts 配置都由 Server.changes 保护。
type hostAPI struct {
	s       *Server
	pending map[int64]fetchedHostKey
}

func (s *Server) hostRoutes() {
	a := &hostAPI{s: s, pending: make(map[int64]fetchedHostKey)}
	s.mux.HandleFunc("GET /api/v1/hosts", a.list)
	s.mux.HandleFunc("POST /api/v1/hosts", a.create)
	s.mux.HandleFunc("GET /api/v1/hosts/{id}", a.get)
	s.mux.HandleFunc("PUT /api/v1/hosts/{id}", a.update)
	s.mux.HandleFunc("DELETE /api/v1/hosts/{id}", a.remove)
	s.mux.HandleFunc("POST /api/v1/hosts/{id}/hostkey/fetch", a.fetchKey)
	s.mux.HandleFunc("POST /api/v1/hosts/{id}/hostkey/confirm", a.confirmKey)
	s.mux.HandleFunc("POST /api/v1/hosts/{id}/test-connection", a.testConnection)
	s.mux.HandleFunc("POST /api/v1/hosts/{id}/test-command", a.testCommand)
	s.mux.HandleFunc("POST /api/v1/hosts/{id}/online-check", a.online)
}

type hostConnectionInput struct {
	Port       *int    `json:"port"`
	Username   *string `json:"username"`
	AuthMethod *string `json:"auth_method"`
	Sudo       *bool   `json:"sudo"`
}
type hostInput struct {
	Name         *string              `json:"name"`
	Address      *string              `json:"address"`
	Kind         *string              `json:"kind"`
	Conn         *hostConnectionInput `json:"conn"`
	OnlineCheck  *host.OnlineCheck    `json:"online_check"`
	Password     string               `json:"password"`
	PrivateKey   string               `json:"private_key"`
	Passphrase   string               `json:"passphrase"`
	ClearSecrets []string             `json:"clear_secrets"`
}

func applyHost(h *host.Record, in hostInput) error {
	oldAddress, oldPort := h.Address, h.Conn.Port
	if in.Name != nil {
		h.Name = *in.Name
	}
	if in.Address != nil {
		h.Address = *in.Address
	}
	if in.Kind != nil {
		h.Kind = *in.Kind
	}
	if in.Conn != nil {
		c := in.Conn
		if c.AuthMethod != nil && *c.AuthMethod != h.Conn.AuthMethod {
			h.Credentials = host.Credentials{}
			h.Conn.AuthMethod = *c.AuthMethod
		}
		if c.Port != nil {
			h.Conn.Port = *c.Port
		}
		if c.Username != nil {
			h.Conn.Username = *c.Username
		}
		if c.Sudo != nil {
			h.Conn.Sudo = *c.Sudo
		}
	}
	if in.OnlineCheck != nil {
		h.OnlineCheck = *in.OnlineCheck
	}
	values := map[string]string{"password": in.Password, "private_key": in.PrivateKey, "passphrase": in.Passphrase}
	seen := map[string]bool{}
	for _, key := range in.ClearSecrets {
		if seen[key] || values[key] != "" {
			return errValidation
		}
		seen[key] = true
		switch key {
		case "password":
			h.Credentials.Password = secret.String{}
		case "private_key":
			h.Credentials.PrivateKey = secret.String{}
		case "passphrase":
			h.Credentials.Passphrase = secret.String{}
		default:
			return errValidation
		}
	}
	if in.Password != "" {
		h.Credentials.Password = secret.NewString(in.Password)
	}
	if in.PrivateKey != "" {
		h.Credentials.PrivateKey = secret.NewString(in.PrivateKey)
	}
	if in.Passphrase != "" {
		h.Credentials.Passphrase = secret.NewString(in.Passphrase)
	}
	if oldAddress != h.Address || oldPort != h.Conn.Port {
		h.Fingerprint = ""
	}
	if host.Validate(*h) != nil {
		return errValidation
	}
	return nil
}

func hostAudit(h host.Record) map[string]any {
	v := h.View()
	// 用散列区分同为已设置状态的凭据轮换；audit 对 secret 整体只记录“已修改”。
	v["secret"] = map[string]string{
		"password":    fmt.Sprintf("%x", sha256.Sum256([]byte(h.Credentials.Password.Reveal()))),
		"private_key": fmt.Sprintf("%x", sha256.Sum256([]byte(h.Credentials.PrivateKey.Reveal()))),
		"passphrase":  fmt.Sprintf("%x", sha256.Sum256([]byte(h.Credentials.Passphrase.Reveal()))),
	}
	return v
}

func (a *hostAPI) list(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := a.s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+host.Columns+" FROM hosts WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		a.s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		h, err := host.Scan(rows, a.s.opts.Cipher)
		if err != nil {
			a.s.internal(w, err)
			return
		}
		items = append(items, h.View())
	}
	if err := rows.Err(); err != nil {
		a.s.internal(w, err)
		return
	}
	next := int64(0)
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1]["id"].(int64)
	}
	page(w, items, next)
}
func (a *hostAPI) get(w http.ResponseWriter, r *http.Request) {
	h, ok := a.read(w, r)
	if ok {
		reply(w, 200, h.View())
	}
}
func (a *hostAPI) read(w http.ResponseWriter, r *http.Request) (host.Record, bool) {
	id, ok := idParam(w, r)
	if !ok {
		return host.Record{}, false
	}
	h, err := host.Read(r.Context(), a.s.opts.DB, a.s.opts.Cipher, id)
	if err != nil {
		a.s.storageError(w, err, "host")
		return host.Record{}, false
	}
	return h, true
}
func (a *hostAPI) create(w http.ResponseWriter, r *http.Request) { a.save(w, r, 0) }
func (a *hostAPI) update(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if ok {
		a.save(w, r, id)
	}
}
func (a *hostAPI) save(w http.ResponseWriter, r *http.Request, id int64) {
	var in hostInput
	if !decode(w, r, &in) {
		return
	}
	a.s.changes.Lock()
	defer a.s.changes.Unlock()
	h := host.Record{Kind: "other", Conn: host.Connection{Port: 22, AuthMethod: "password"}, OnlineCheck: host.OnlineCheck{Type: "tcp", Port: 22}}
	var before any
	if id != 0 {
		var err error
		h, err = host.Read(r.Context(), a.s.opts.DB, a.s.opts.Cipher, id)
		if err != nil {
			a.s.storageError(w, err, "host")
			return
		}
		before = hostAudit(h)
	}
	if err := applyHost(&h, in); err != nil {
		a.s.storageError(w, err, "host")
		return
	}
	conn, encrypted, online, err := host.Encode(h, a.s.opts.Cipher)
	if err != nil {
		a.s.internal(w, err)
		return
	}
	err = a.s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		kind := "host.update"
		if id == 0 {
			kind = "host.create"
			result, err := tx.ExecContext(r.Context(), "INSERT INTO hosts(name,address,kind,conn,secret,hostkey_fingerprint,online_check) VALUES(?,?,?,?,?,?,?)", h.Name, h.Address, h.Kind, conn, encrypted, h.Fingerprint, online)
			if err != nil {
				return err
			}
			h.ID, err = result.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(r.Context(), "UPDATE hosts SET name=?,address=?,kind=?,conn=?,secret=?,hostkey_fingerprint=?,online_check=? WHERE id=?", h.Name, h.Address, h.Kind, conn, encrypted, h.Fingerprint, online, id); err != nil {
				return err
			}
		}
		return logChange(r.Context(), tx, r, kind, strconv.FormatInt(h.ID, 10), before, hostAudit(h))
	})
	if err != nil {
		a.s.storageError(w, err, "host")
		return
	}
	delete(a.pending, h.ID)
	code := 200
	if id == 0 {
		code = 201
	}
	reply(w, code, h.View())
}
func (a *hostAPI) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	a.s.changes.Lock()
	defer a.s.changes.Unlock()
	h, err := host.Read(r.Context(), a.s.opts.DB, a.s.opts.Cipher, id)
	if err != nil {
		a.s.storageError(w, err, "host")
		return
	}
	err = a.s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		var refs int
		if err := tx.QueryRowContext(r.Context(), `SELECT (SELECT count(*) FROM actions WHERE CAST(json_extract(config,'$.host_id') AS INTEGER)=?) + (SELECT count(*) FROM plan_run_steps WHERE host_id=?)`, id, id).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			return errConflict
		}
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM hosts WHERE id=?", id); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "host.delete", strconv.FormatInt(id, 10), hostAudit(h), nil)
	})
	if err != nil {
		a.s.storageError(w, err, "host")
		return
	}
	delete(a.pending, id)
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *hostAPI) requested(r *http.Request, id int64, kind string) error {
	return a.s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		return audit.Write(r.Context(), tx, audit.Record{UserID: current(r).User.ID, IP: remoteIP(r), Kind: kind, Target: strconv.FormatInt(id, 10), Result: "requested"})
	})
}
func (a *hostAPI) fetchKey(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	h, ok := a.read(w, r)
	if !ok {
		return
	}
	if err := a.requested(r, h.ID, "host.hostkey.fetch"); err != nil {
		a.s.internal(w, err)
		return
	}
	fp, err := host.FetchHostKey(r.Context(), h)
	if err != nil {
		a.operationError(w, err)
		return
	}
	a.s.changes.Lock()
	defer a.s.changes.Unlock()
	currentHost, err := host.Read(r.Context(), a.s.opts.DB, a.s.opts.Cipher, h.ID)
	if err != nil {
		a.s.storageError(w, err, "host")
		return
	}
	if h.Address != currentHost.Address || h.Conn.Port != currentHost.Conn.Port {
		fail(w, 409, "host_changed", "获取指纹期间地址或端口已变化，请重新获取")
		return
	}
	now := time.Now()
	for id, p := range a.pending {
		if !now.Before(p.expires) {
			delete(a.pending, id)
		}
	}
	expires := now.Add(hostKeyTTL)
	a.pending[h.ID] = fetchedHostKey{h.Address, h.Conn.Port, fp, expires}
	reply(w, 200, map[string]any{"fingerprint": fp, "expires_at": expires.UTC()})
}
func (a *hostAPI) confirmKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm     bool   `json:"confirm"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !in.Confirm {
		fail(w, 422, "confirmation_required", "需要 confirm=true")
		return
	}
	a.s.changes.Lock()
	defer a.s.changes.Unlock()
	h, ok := a.read(w, r)
	if !ok {
		return
	}
	pending, ok := a.pending[h.ID]
	if !ok || !time.Now().Before(pending.expires) || pending.address != h.Address || pending.port != h.Conn.Port || pending.fingerprint != in.Fingerprint {
		fail(w, 409, "hostkey_candidate_invalid", "必须先重新获取当前地址的指纹，并在 5 分钟内确认实际获取的指纹")
		return
	}
	before := hostAudit(h)
	h.Fingerprint = pending.fingerprint
	err := a.s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), "UPDATE hosts SET hostkey_fingerprint=? WHERE id=?", h.Fingerprint, h.ID); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "host.hostkey.confirm", strconv.FormatInt(h.ID, 10), before, hostAudit(h))
	})
	if err != nil {
		a.s.storageError(w, err, "host")
		return
	}
	delete(a.pending, h.ID)
	reply(w, 200, h.View())
}
func (a *hostAPI) testConnection(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	h, ok := a.read(w, r)
	if !ok {
		return
	}
	if err := a.requested(r, h.ID, "host.test_connection"); err != nil {
		a.s.internal(w, err)
		return
	}
	if err := host.TestConnection(r.Context(), h); err != nil {
		a.operationError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *hostAPI) testCommand(w http.ResponseWriter, r *http.Request) {
	if !confirm(w, r) {
		return
	}
	h, ok := a.read(w, r)
	if !ok {
		return
	}
	if err := a.requested(r, h.ID, "host.test_command"); err != nil {
		a.s.internal(w, err)
		return
	}
	result, err := host.TestCommand(r.Context(), h)
	if err != nil {
		a.operationError(w, err)
		return
	}
	reply(w, 200, result)
}
func (a *hostAPI) online(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	h, ok := a.read(w, r)
	if !ok {
		return
	}
	if err := a.requested(r, h.ID, "host.online_check"); err != nil {
		a.s.internal(w, err)
		return
	}
	online, err := host.CheckOnline(r.Context(), h)
	if err != nil {
		a.operationError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"online": online})
}
func (a *hostAPI) operationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, host.ErrUnconfirmed):
		fail(w, 409, "hostkey_unconfirmed", host.ErrUnconfirmed.Error())
	case errors.Is(err, host.ErrFingerprint):
		fail(w, 409, "hostkey_mismatch", host.ErrFingerprint.Error())
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, 504, "host_timeout", "主机操作超时")
	case errors.Is(err, context.Canceled):
		fail(w, 408, "host_cancelled", "主机操作已取消")
	default:
		// host 模块只返回不含远端文本与凭据的安全错误；保留 ICMP 权限提示。
		fail(w, 422, "host_operation_failed", err.Error())
	}
}
