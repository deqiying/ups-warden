package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/store"
)

const testPassword = "测试密码abcd1234"

func testService(t *testing.T, opts Options) (*Service, *store.Store) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := New(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}
func setup(t *testing.T, s *Service) User {
	t.Helper()
	u, err := s.Setup(t.Context(), s.SetupToken(), "Admin", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func login(t *testing.T, s *Service, name string) Session {
	t.Helper()
	session, err := s.Login(t.Context(), name, testPassword, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	return session
}
func exec(t *testing.T, db *store.Store, q string, args ...any) {
	t.Helper()
	if _, err := db.Write().ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func count(t *testing.T, db *store.Store, table string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func wantError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSetupConcurrentAndRestart(t *testing.T) {
	s, db := testService(t, Options{})
	token := s.SetupToken()
	if !validToken(token) {
		t.Fatal("invalid setup token")
	}
	s2, err := New(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if token == s2.SetupToken() {
		t.Fatal("restart reused token")
	}
	_, err = s.Setup(t.Context(), "wrong", "admin", testPassword, "")
	wantError(t, err, ErrInvalidSetupToken)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Go(func() { _, err := s.Setup(t.Context(), token, "Admin", testPassword, ""); errs <- err })
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			wantError(t, err, ErrSetupClosed)
		}
	}
	if success != 1 || count(t, db, "users") != 1 {
		t.Fatal("setup not exclusive")
	}
	if s.SetupToken() != "" {
		t.Fatal("setup token not cleared")
	}
	needs, err := s.NeedsSetup(t.Context())
	if err != nil || needs {
		t.Fatalf("needs setup %v %v", needs, err)
	}
	s3, err := New(db, Options{})
	if err != nil || s3.SetupToken() != "" {
		t.Fatalf("setup reopened: %v", err)
	}
	_, err = s.Setup(t.Context(), token, "another", testPassword, "")
	wantError(t, err, ErrSetupClosed)
	_, err = s2.Setup(t.Context(), s2.SetupToken(), "another", testPassword, "")
	wantError(t, err, ErrSetupClosed)
}
func TestPasswordValidationAndHashBounds(t *testing.T) {
	for _, p := range []string{"1234567", strings.Repeat("a", 1025), string([]byte{0xff}) + "12345678"} {
		if validPassword(p) {
			t.Fatal("accepted invalid password")
		}
	}
	if !validPassword("八个汉字作为密码") || !validPassword(strings.Repeat("a", 1024)) {
		t.Fatal("valid password rejected")
	}
	for _, name := range []string{"", strings.Repeat("a", 65), "你好", "a b", "a/b"} {
		_, err := username(name)
		wantError(t, err, ErrValidation)
	}
	if n, err := username("AbC._-123"); err != nil || n != "abc._-123" {
		t.Fatal(n, err)
	}
	h, err := passwordHash(t.Context(), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") || strings.Contains(h, testPassword) {
		t.Fatal("hash parameters")
	}
	ok, err := verify(t.Context(), testPassword, h)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ok, err = verify(t.Context(), "wrong", h)
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 201), strings.Replace(h, "m=65536", "m=4294967295", 1), strings.Replace(h, "t=3", "t=9999", 1), strings.Replace(h, "p=2", "p=255", 1), strings.Replace(h, "v=19", "v=20", 1), h + "$bad"} {
		if _, err := verify(t.Context(), testPassword, bad); err == nil {
			t.Fatal("accepted damaged hash")
		}
	}
}
func TestSessionsAndUserManagement(t *testing.T) {
	s, db := testService(t, Options{})
	admin := setup(t, s)
	a := login(t, s, "ADMIN")
	b := login(t, s, "admin")
	if a.User.LastLoginAt == nil || a.User.Username != "admin" || a.Token == b.Token || a.CSRF == b.CSRF || !validToken(a.Token) || !validToken(a.CSRF) {
		t.Fatal("invalid login result")
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), a.Token) || strings.Contains(string(data), "password") {
		t.Fatal("session JSON leak")
	}
	var stored string
	if err := db.Read().QueryRowContext(t.Context(), `SELECT token_hash FROM sessions WHERE token_hash=?`, tokenHash(a.Token)).Scan(&stored); err != nil || stored == a.Token {
		t.Fatal(stored, err)
	}
	if _, err := s.Authenticate(t.Context(), stored); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("accepted token hash")
	}
	auth, err := s.Authenticate(t.Context(), a.Token)
	if err != nil || auth.User.ID != admin.ID || auth.CSRF != a.CSRF {
		t.Fatal(auth, err)
	}
	wantError(t, s.DeleteUser(t.Context(), admin.ID, admin.ID, ""), ErrLastAdmin)
	second, err := s.CreateUser(t.Context(), admin.ID, "", "SECOND", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateUser(t.Context(), admin.ID, "", "Second", testPassword)
	wantError(t, err, ErrConflict)
	_, err = s.CreateUser(t.Context(), 999, "", "denied", testPassword)
	wantError(t, err, ErrInvalidCredentials)
	wantError(t, s.DeleteUser(t.Context(), admin.ID, admin.ID, ""), ErrValidation)
	users, err := s.Users(t.Context(), 0, 1)
	if err != nil || len(users) != 1 || users[0].ID != second.ID {
		t.Fatal(users, err)
	}
	users, err = s.Users(t.Context(), second.ID, 100)
	if err != nil || len(users) != 1 || users[0].ID != admin.ID {
		t.Fatal(users, err)
	}
	_, err = s.Users(t.Context(), 0, 0)
	wantError(t, err, ErrValidation)
	renamed, err := s.RenameUser(t.Context(), admin.ID, second.ID, "", "Renamed")
	if err != nil || renamed.Username != "renamed" {
		t.Fatal(renamed, err)
	}
	_, err = s.RenameUser(t.Context(), admin.ID, second.ID, "", "ADMIN")
	wantError(t, err, ErrConflict)
	_, err = s.RenameUser(t.Context(), admin.ID, 999, "", "missing")
	wantError(t, err, ErrNotFound)
	other := login(t, s, "renamed")
	if err := s.ChangePassword(t.Context(), admin.ID, admin.ID, a.Token, "", "new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(t.Context(), a.Token); err != nil {
		t.Fatal("current session lost", err)
	}
	_, err = s.Authenticate(t.Context(), b.Token)
	wantError(t, err, ErrInvalidCredentials)
	_, err = s.Login(t.Context(), "admin", testPassword, "127.0.0.2", "")
	wantError(t, err, ErrInvalidCredentials)
	if _, err := s.Login(t.Context(), "admin", "new-password", "127.0.0.2", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(t.Context(), admin.ID, second.ID, other.Token, "", "other-password"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(t.Context(), other.Token)
	wantError(t, err, ErrInvalidCredentials)
	if err := s.DeleteUser(t.Context(), admin.ID, second.ID, ""); err != nil {
		t.Fatal(err)
	}
	wantError(t, s.DeleteUser(t.Context(), admin.ID, second.ID, ""), ErrNotFound)
	if err := s.Logout(t.Context(), a.Token, ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(t.Context(), a.Token)
	wantError(t, err, ErrInvalidCredentials)
	if err := s.Logout(t.Context(), a.Token, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Read().QueryContext(t.Context(), `SELECT diff FROM audit_logs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var diff string
		if err := rows.Scan(&diff); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(diff, "new-password") || strings.Contains(diff, "other-password") || strings.Contains(diff, testPassword) {
			t.Fatal("audit password leak")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
func TestSessionTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := testService(t, Options{})
		setup(t, s)
		idle := login(t, s, "admin")
		active := login(t, s, "admin")
		time.Sleep(23 * time.Hour)
		if _, err := s.Authenticate(t.Context(), active.Token); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		_, err := s.Authenticate(t.Context(), idle.Token)
		wantError(t, err, ErrInvalidCredentials)
		if _, err := s.Authenticate(t.Context(), active.Token); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 6; i++ {
			time.Sleep(23 * time.Hour)
			if _, err := s.Authenticate(t.Context(), active.Token); err != nil {
				t.Fatal(i, err)
			}
		}
		time.Sleep(6 * time.Hour)
		_, err = s.Authenticate(t.Context(), active.Token)
		wantError(t, err, ErrInvalidCredentials)
	})
}
func TestRateLimitsAndExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := testService(t, Options{})
		setup(t, s)
		for i := 0; i < 5; i++ {
			_, err := s.Login(t.Context(), "ADMIN", "wrong", fmt.Sprintf("192.0.2.%d", i), "")
			wantError(t, err, ErrInvalidCredentials)
		}
		_, err := s.Login(t.Context(), "admin", testPassword, "new-ip", "")
		wantError(t, err, ErrRateLimited)
		time.Sleep(15 * time.Minute)
		if _, err := s.Login(t.Context(), "admin", testPassword, "new-ip", ""); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			_, err := s.Login(t.Context(), fmt.Sprintf("missing%d", i), "wrong", "same-ip", "")
			wantError(t, err, ErrInvalidCredentials)
		}
		_, err = s.Login(t.Context(), "admin", testPassword, "same-ip", "")
		wantError(t, err, ErrRateLimited)
		time.Sleep(15 * time.Minute)
		if _, err := s.Login(t.Context(), "admin", testPassword, "same-ip", ""); err != nil {
			t.Fatal(err)
		}
	})
}
func TestLimiterBoundedAndConcurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var l limiter
		now := time.Now()
		keys, err := l.reserve("same", "same", now)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			if _, err := l.reserve("same", "same", now); err != nil {
				t.Fatal(err)
			}
		}
		_, err = l.reserve("same", "same", now)
		wantError(t, err, ErrRateLimited)
		for i := 0; i < 5; i++ {
			l.finish(keys, now, true)
		}
		time.Sleep(15 * time.Minute)
		if _, err := l.reserve("same", "same", time.Now()); err != nil {
			t.Fatal(err)
		}
		var bounded limiter
		for i := 0; i < maxRateKeys/2; i++ {
			k, err := bounded.reserve(fmt.Sprint(i), fmt.Sprint(i), now)
			if err != nil {
				t.Fatal(err)
			}
			bounded.finish(k, now, true)
		}
		_, err = bounded.reserve("overflow", "overflow", now)
		wantError(t, err, ErrRateLimited)
		if len(bounded.entries) != maxRateKeys {
			t.Fatal("wrong bound")
		}
		if _, err := bounded.reserve("after", "after", time.Now()); err != nil {
			t.Fatal("expired keys retained", err)
		}
	})
}
func TestAuditFailureRollsBack(t *testing.T) {
	s, db := testService(t, Options{})
	exec(t, db, `CREATE TRIGGER reject_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	token := s.SetupToken()
	if _, err := s.Setup(t.Context(), token, "admin", testPassword, ""); err == nil {
		t.Fatal("setup succeeded without audit")
	}
	if count(t, db, "users") != 0 || s.SetupToken() != token {
		t.Fatal("setup did not roll back")
	}
	exec(t, db, `DROP TRIGGER reject_audit`)
	u := setup(t, s)
	session := login(t, s, "admin")
	exec(t, db, `UPDATE users SET last_login_at=NULL WHERE id=?`, u.ID)
	exec(t, db, `CREATE TRIGGER reject_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	if _, err := s.Login(t.Context(), "admin", testPassword, "", ""); err == nil {
		t.Fatal("login without audit")
	}
	if count(t, db, "sessions") != 1 {
		t.Fatal("login session not rolled back")
	}
	var last sql.NullInt64
	if err := db.Read().QueryRowContext(t.Context(), `SELECT last_login_at FROM users WHERE id=?`, u.ID).Scan(&last); err != nil || last.Valid {
		t.Fatal("last_login not rolled back", err)
	}
	if err := s.Logout(t.Context(), session.Token, ""); err == nil {
		t.Fatal("logout without audit")
	}
	if _, err := s.Authenticate(t.Context(), session.Token); err != nil {
		t.Fatal("logout not rolled back", err)
	}
	if _, err := s.CreateUser(t.Context(), u.ID, "", "second", testPassword); err == nil {
		t.Fatal("create without audit")
	}
	if count(t, db, "users") != 1 {
		t.Fatal("create not rolled back")
	}
	if _, err := s.RenameUser(t.Context(), u.ID, u.ID, "", "changed"); err == nil {
		t.Fatal("rename without audit")
	}
	if err := s.ChangePassword(t.Context(), u.ID, u.ID, "", "", "changed-password"); err == nil {
		t.Fatal("password change without audit")
	}
	if _, err := s.Authenticate(t.Context(), session.Token); err != nil {
		t.Fatal("password sessions not rolled back", err)
	}
	exec(t, db, `DROP TRIGGER reject_audit`)
	login(t, s, "admin")
	second, err := s.CreateUser(t.Context(), u.ID, "", "second", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	other := login(t, s, "second")
	exec(t, db, `CREATE TRIGGER reject_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	if err := s.DeleteUser(t.Context(), u.ID, second.ID, ""); err == nil {
		t.Fatal("delete without audit")
	}
	if _, err := s.Authenticate(t.Context(), other.Token); err != nil {
		t.Fatal("delete cascade not rolled back", err)
	}
}
func TestCancellation(t *testing.T) {
	s, _ := testService(t, Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Setup(ctx, s.SetupToken(), "admin", testPassword, "")
	wantError(t, err, context.Canceled)
	setup(t, s)
	a := login(t, s, "admin")
	_, err = s.Login(ctx, "admin", testPassword, "", "")
	wantError(t, err, context.Canceled)
	_, err = s.Authenticate(ctx, a.Token)
	wantError(t, err, context.Canceled)
	wantError(t, s.Logout(ctx, a.Token, ""), context.Canceled)
	_, err = s.Users(ctx, 0, 10)
	wantError(t, err, context.Canceled)
	_, err = s.CreateUser(ctx, a.User.ID, "", "second", testPassword)
	wantError(t, err, context.Canceled)
	_, err = s.RenameUser(ctx, a.User.ID, a.User.ID, "", "renamed")
	wantError(t, err, context.Canceled)
	wantError(t, s.DeleteUser(ctx, a.User.ID, a.User.ID, ""), context.Canceled)
	wantError(t, s.ChangePassword(ctx, a.User.ID, a.User.ID, a.Token, "", testPassword), context.Canceled)
	// 两个计算槽满时，等待者仍可取消；不启动耗时哈希。
	hashSlots <- struct{}{}
	hashSlots <- struct{}{}
	_, err = derive(ctx, testPassword, make([]byte, 16), 65536, 3, 2)
	<-hashSlots
	<-hashSlots
	wantError(t, err, context.Canceled)
}

// observingContext 仅同步观察等待槽位的 select，不使用真实时间或替换全局槽位。
type observingContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}
func TestHashWaitCancellation(t *testing.T) {
	hashSlots <- struct{}{}
	hashSlots <- struct{}{}
	defer func() { <-hashSlots; <-hashSlots }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := &observingContext{Context: ctx, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := derive(observed, testPassword, make([]byte, 16), 65536, 3, 2); done <- err }()
	<-observed.entered
	cancel()
	wantError(t, <-done, context.Canceled)
}

func TestLimiterSuccessDoesNotEraseFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var l limiter
		for i := 0; i < 4; i++ {
			keys, err := l.reserve(fmt.Sprint(i), "shared-ip", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			l.finish(keys, time.Now(), true)
		}
		keys, err := l.reserve("valid-user", "shared-ip", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		l.finish(keys, time.Now(), false)
		keys, err = l.reserve("fifth", "shared-ip", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		l.finish(keys, time.Now(), true)
		_, err = l.reserve("new-user", "shared-ip", time.Now())
		wantError(t, err, ErrRateLimited)
	})
}
