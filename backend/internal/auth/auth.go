// Package auth 提供管理员、首次初始化和服务端会话；不处理 HTTP 或 Cookie。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"ups-warden/internal/audit"
	"ups-warden/internal/store"
)

// 可使用 errors.Is 判断认证、验证、限速及用户管理失败。
var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrRateLimited        = errors.New("登录尝试过多，请稍后重试")
	ErrSetupClosed        = errors.New("初始化已关闭")
	ErrInvalidSetupToken  = errors.New("初始化令牌无效")
	ErrValidation         = errors.New("参数无效")
	ErrConflict           = errors.New("用户名已存在")
	ErrNotFound           = errors.New("用户不存在")
	ErrLastAdmin          = errors.New("不能删除最后一个管理员")
)

// User 不包含密码或密码哈希。
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

// Session 的原始 Token 仅供内部设置 Cookie，JSON 只可输出 CSRF。
type Session struct {
	User      User      `json:"user"`
	Token     string    `json:"-"`
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expires_at"`
	// AbsoluteExpiresAt 用于 Cookie，SSE 活跃期间不能续发响应头；空闲期限仍由服务端执行。
	AbsoluteExpiresAt time.Time `json:"-"`
}

// Options 的 Now 默认为 time.Now，注入函数须并发安全。
type Options struct{ Now func() time.Time }

// Service 可并发使用，限速和初始化令牌只保存在本进程内存。
type Service struct {
	db         *store.Store
	now        func() time.Time
	setupMu    sync.Mutex
	setupToken string
	rates      limiter
}

// New 检查用户表，无用户时生成仅内存的初始化令牌，不输出日志。
func New(db *store.Store, opts Options) (*Service, error) {
	if db == nil {
		return nil, ErrValidation
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := &Service{db: db, now: now}
	needs, err := s.NeedsSetup(context.Background())
	if err != nil {
		return nil, err
	}
	if needs {
		s.setupToken, err = randomToken()
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SetupToken 返回内存令牌；初始化成功后为空，由主服务负责仅输出一次。
func (s *Service) SetupToken() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupToken
}

// NeedsSetup 实时查询是否没有用户。
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	var n int
	err := s.db.Read().QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n == 0, err
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: 生成随机令牌: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(token string) string {
	b := sha256.Sum256([]byte(token))
	return hex.EncodeToString(b[:])
}
func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == token
}

const userColumns = `id,username,role,created_at,last_login_at`

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	var created int64
	var login sql.NullInt64
	err := row.Scan(&u.ID, &u.Username, &u.Role, &created, &login)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	if login.Valid {
		t := time.UnixMilli(login.Int64).UTC()
		u.LastLoginAt = &t
	}
	return u, nil
}
func userByID(ctx context.Context, tx *sql.Tx, id int64) (User, error) {
	return scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=?`, id))
}
func admin(ctx context.Context, tx *sql.Tx, id int64) error {
	u, err := userByID(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if u.Role != "admin" {
		return ErrInvalidCredentials
	}
	return nil
}
func available(ctx context.Context, tx *sql.Tx, name string, except int64) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE lower(username)=? AND id<>?`, name, except).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrConflict
	}
	return nil
}
func insertUser(ctx context.Context, tx *sql.Tx, name, hash string, now time.Time) (User, error) {
	if err := available(ctx, tx, name, 0); err != nil {
		return User{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,'admin',?)`, name, hash, now.UnixMilli())
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return userByID(ctx, tx, id)
}
func record(ctx context.Context, tx *sql.Tx, actor int64, ip, kind string, id int64, before, after map[string]any) error {
	return audit.Write(ctx, tx, audit.Record{UserID: actor, IP: ip, Kind: kind, Target: "user:" + strconv.FormatInt(id, 10), Result: "success", Before: before, After: after})
}

// Setup 在单个写事务中检查空库并创建首位管理员；并发只能成功一次。
func (s *Service) Setup(ctx context.Context, token, name, password, ip string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.setupMu.Lock()
	expected := s.setupToken
	s.setupMu.Unlock()
	if expected == "" {
		return User{}, ErrSetupClosed
	}
	a, b := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(expected))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return User{}, ErrInvalidSetupToken
	}
	name, err := username(name)
	if err != nil {
		return User{}, err
	}
	if len(ip) > 128 {
		return User{}, ErrValidation
	}
	hash, err := passwordHash(ctx, password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return ErrSetupClosed
		}
		var err error
		u, err = insertUser(ctx, tx, name, hash, s.now().UTC())
		if err != nil {
			return err
		}
		return record(ctx, tx, u.ID, ip, "setup", u.ID, nil, map[string]any{"username": u.Username, "role": u.Role})
	})
	if err != nil {
		return User{}, err
	}
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	return u, nil
}

// Login 计数依据规范化用户名和传入的来源 IP，不读取代理 Header。
func (s *Service) Login(ctx context.Context, name, password, ip, userAgent string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	name, err := username(name)
	if err != nil || len(password) > 1024 || len(ip) > 128 || len(userAgent) > 2048 {
		return Session{}, ErrInvalidCredentials
	}
	keys, err := s.rates.reserve(name, ip, s.now())
	if err != nil {
		return Session{}, err
	}
	failed := false
	defer func() { s.rates.finish(keys, s.now(), failed) }()
	var id int64
	var hash string
	err = s.db.Read().QueryRowContext(ctx, `SELECT id,password_hash FROM users WHERE lower(username)=?`, name).Scan(&id, &hash)
	matched := false
	if errors.Is(err, sql.ErrNoRows) {
		err = dummy(ctx, password)
	} else if err == nil {
		matched, err = verify(ctx, password, hash)
	}
	if err != nil {
		return Session{}, err
	}
	if !matched {
		failed = true
		err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
			return audit.Write(ctx, tx, audit.Record{IP: ip, Kind: "login", Target: "user:" + name, Result: "failure"})
		})
		if err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidCredentials
	}
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	var session Session
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		// 防止哈希验证期间密码被修改或用户被删除后仍建立旧密码会话。
		var current string
		err := tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=?`, id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) || err == nil && current != hash {
			return ErrInvalidCredentials
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		expires := now.Add(7 * 24 * time.Hour)
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=? OR last_seen_at<=?`, now.UnixMilli(), now.Add(-24*time.Hour).UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,csrf_token,created_at,expires_at,last_seen_at,ip,user_agent) VALUES(?,?,?,?,?,?,?,?)`, tokenHash(token), id, csrf, now.UnixMilli(), expires.UnixMilli(), now.UnixMilli(), ip, userAgent); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, now.UnixMilli(), id); err != nil {
			return err
		}
		u, err := userByID(ctx, tx, id)
		if err != nil {
			return err
		}
		session = Session{User: u, Token: token, CSRF: csrf, ExpiresAt: now.Add(24 * time.Hour), AbsoluteExpiresAt: now.Add(7 * 24 * time.Hour)}
		return record(ctx, tx, id, ip, "login", id, nil, nil)
	})
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

// Authenticate 接受原始 Cookie 令牌，原子校验期限并刷新空闲计时。
// ExpiresAt 是当前空闲与绝对期限的较早值；Token 永远不序列化。
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if !validToken(token) {
		return Session{}, ErrInvalidCredentials
	}
	var session Session
	invalid := false
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var id, created, expires, last int64
		var csrf string
		err := tx.QueryRowContext(ctx, `SELECT user_id,csrf_token,created_at,expires_at,last_seen_at FROM sessions WHERE token_hash=?`, tokenHash(token)).Scan(&id, &csrf, &created, &expires, &last)
		if errors.Is(err, sql.ErrNoRows) {
			invalid = true
			return nil
		}
		if err != nil {
			return err
		}
		now := s.now().UTC()
		absolute := time.UnixMilli(created).Add(7 * 24 * time.Hour)
		stored := time.UnixMilli(expires)
		if stored.Before(absolute) {
			absolute = stored
		}
		if !now.Before(absolute) || !now.Before(time.UnixMilli(last).Add(24*time.Hour)) {
			invalid = true
			_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(token))
			return err
		}
		u, err := userByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE token_hash=?`, now.UnixMilli(), tokenHash(token)); err != nil {
			return err
		}
		effective := now.Add(24 * time.Hour)
		if absolute.Before(effective) {
			effective = absolute
		}
		session = Session{User: u, Token: token, CSRF: csrf, ExpiresAt: effective, AbsoluteExpiresAt: absolute}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if invalid {
		return Session{}, ErrInvalidCredentials
	}
	return session, nil
}

// Logout 删除会话并在同事务审计；不存在的会话幂等成功。
func (s *Service) Logout(ctx context.Context, token, ip string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ip) > 128 {
		return ErrValidation
	}
	if !validToken(token) {
		return nil
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE token_hash=?`, tokenHash(token)).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(token)); err != nil {
			return err
		}
		return record(ctx, tx, id, ip, "logout", id, nil, nil)
	})
}

// Users 按 id 降序返回游标页；before=0 为第一页，limit 为 1..201（含分页探测行）。
func (s *Service) Users(ctx context.Context, before int64, limit int) ([]User, error) {
	if before < 0 || limit < 1 || limit > 201 {
		return nil, ErrValidation
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateUser 创建另一个管理员，actorID 必须是现存管理员。
func (s *Service) CreateUser(ctx context.Context, actorID int64, ip, name, password string) (User, error) {
	name, err := username(name)
	if err != nil {
		return User{}, err
	}
	if len(ip) > 128 {
		return User{}, ErrValidation
	}
	hash, err := passwordHash(ctx, password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, actorID); err != nil {
			return err
		}
		var err error
		u, err = insertUser(ctx, tx, name, hash, s.now().UTC())
		if err != nil {
			return err
		}
		return record(ctx, tx, actorID, ip, "user.create", u.ID, nil, map[string]any{"username": u.Username, "role": u.Role})
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// RenameUser 规范化名称，唯一性检查与修改和审计在同一事务。
func (s *Service) RenameUser(ctx context.Context, actorID, id int64, ip, name string) (User, error) {
	name, err := username(name)
	if err != nil {
		return User{}, err
	}
	if len(ip) > 128 {
		return User{}, ErrValidation
	}
	var u User
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, actorID); err != nil {
			return err
		}
		old, err := userByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := available(ctx, tx, name, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET username=? WHERE id=?`, name, id); err != nil {
			return err
		}
		u = old
		u.Username = name
		return record(ctx, tx, actorID, ip, "user.rename", id, map[string]any{"username": old.Username}, map[string]any{"username": name})
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// DeleteUser 拒绝删除最后管理员和本人；会话由 schema 的外键级联删除。
func (s *Service) DeleteUser(ctx context.Context, actorID, id int64, ip string) error {
	if len(ip) > 128 {
		return ErrValidation
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, actorID); err != nil {
			return err
		}
		u, err := userByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if u.Role == "admin" {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin'`).Scan(&n); err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		if actorID == id {
			return ErrValidation
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id); err != nil {
			return err
		}
		return record(ctx, tx, actorID, ip, "user.delete", id, map[string]any{"username": u.Username, "role": u.Role}, nil)
	})
}

// ChangePassword 修改自己时仅保留属于自己的 currentToken 会话；修改他人时全部撤销。
func (s *Service) ChangePassword(ctx context.Context, actorID, id int64, currentToken, ip, password string) error {
	if len(ip) > 128 {
		return ErrValidation
	}
	hash, err := passwordHash(ctx, password)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, actorID); err != nil {
			return err
		}
		if _, err := userByID(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, id); err != nil {
			return err
		}
		keep := ""
		if actorID == id && validToken(currentToken) {
			keep = tokenHash(currentToken)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, id, keep); err != nil {
			return err
		}
		return record(ctx, tx, actorID, ip, "user.password", id, nil, map[string]any{"password": "已修改"})
	})
}
