package api

import (
	"context"
	"errors"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestCookieSurvivesActiveStreamBeyondIdleWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newAPI(t)
		f.login(t)
		w := f.req("POST", "/api/v1/auth/login", `{"username":"admin","password":"test-password"}`)
		status(t, w, 200)
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse("http://localhost")
		cookies := w.Result().Cookies()
		jar.SetCookies(u, cookies)
		token := cookies[0].Value
		// 模拟 SSE 心跳只更新服务端活跃时间，不可能在已发送响应头后续发 Cookie。
		time.Sleep(23 * time.Hour)
		if _, err := f.s.opts.Auth.Authenticate(context.Background(), token); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Hour)
		if got := jar.Cookies(u); len(got) != 1 || got[0].Value != token {
			t.Fatal("活跃会话的浏览器 Cookie 在 24 小时后被删除")
		}
		f.token = token
		status(t, f.req("GET", "/api/v1/auth/session", nil), 200)
		time.Sleep(24 * time.Hour)
		status(t, f.req("GET", "/api/v1/auth/session", nil), 401)
	})
}

func TestUsersMaximumPage(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	status(t, f.req("GET", "/api/v1/users?limit=200", nil), 200)
}

type failedRecoveryRuntime struct{ snapshotRuntime }

func (r *failedRecoveryRuntime) ChangeDevices(_ context.Context, _ []int64, change func() error) error {
	if err := change(); err != nil {
		return err
	}
	return errors.New("injected recovery failure")
}

func TestSavedConfigurationFailureIsExplicit(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	f.s.opts.Runtime = &failedRecoveryRuntime{}
	w := f.req("POST", "/api/v1/devices", `{"name":"saved"}`)
	status(t, w, 503)
	if !strings.Contains(w.Body.String(), `"code":"configuration_saved_runtime_error"`) || strings.Contains(w.Body.String(), "injected") {
		t.Fatal(w.Body.String())
	}
	w = f.req("GET", "/api/v1/devices/1", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"name":"saved"`) {
		t.Fatal("已保存配置丢失")
	}
}
