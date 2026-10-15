package api

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"ups-warden/internal/sim"
)

func TestProbeReportsPasswordWrongWithoutCredential(t *testing.T) {
	server, err := sim.Start(sim.Config{Password: "sim-correct"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	_, _, address := server.Addresses()
	host, textPort, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(textPort)
	if err != nil {
		t.Fatal(err)
	}
	f := newAPI(t)
	f.login(t)
	w := f.req("POST", "/api/v1/sources/test", map[string]any{
		"protocol": "ayi9-json", "host": host, "port": port,
		"profile_key": "ayi9-json-generic", "password": "sim-wrong",
	})
	status(t, w, 502)
	body := w.Body.String()
	if !strings.Contains(body, `"code":"source_password_wrong"`) || !strings.Contains(body, "103") {
		t.Fatal("密码错误被泛化", body)
	}
	if strings.Contains(body, "sim-wrong") || strings.Contains(body, "sim-correct") {
		t.Fatal("密码出现在响应中")
	}
	for _, request := range server.Requests() {
		if !request.Allowed {
			t.Fatal("模拟器收到非只读请求")
		}
	}
}
