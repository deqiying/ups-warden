package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/sim"
)

func TestRequiresExplicitAuthorization(t *testing.T) {
	for _, args := range [][]string{nil, {"--profile", "et1k-snmp"}, {"--confirm-readonly"}, {"--profile", "et1k-snmp", "--confirm-readonly", "--timeout", "0s"}, {"--profile", "missing", "--confirm-readonly"}} {
		called := false
		err := run(context.Background(), args, func(string) string { called = true; return "" }, &bytes.Buffer{})
		if err == nil || called {
			t.Fatal("无效参数不能读取连接环境或访问网络")
		}
	}
	if err := run(context.Background(), []string{"--profile", "et1k-snmp", "--confirm-readonly"}, func(string) string { return "" }, &bytes.Buffer{}); err == nil {
		t.Fatal("缺地址未拒绝")
	}
}
func TestProbeOnlyLoopbackSimulator(t *testing.T) {
	server, err := sim.Start(sim.Config{ET1K: true, Community: "private-test", Password: "private-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	sa, ma, aa := server.Addresses()
	for key, addr := range map[string]string{"et1k-snmp": sa, "et1k-modbus": ma, "et1k-ayi9-json": aa} {
		t.Run(key, func(t *testing.T) {
			getenv := func(name string) string {
				switch name {
				case "UPS_WARDEN_PROBE_ADDRESS":
					return addr
				case "UPS_WARDEN_PROBE_COMMUNITY", "UPS_WARDEN_PROBE_PASSWORD":
					return "private-test"
				}
				return ""
			}
			var output bytes.Buffer
			if err := run(context.Background(), []string{"--profile", key, "--confirm-readonly"}, getenv, &output); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if json.Unmarshal(output.Bytes(), &result) != nil || result["profile"] != key || result["fields"] == nil {
				t.Fatal("无效输出")
			}
			for _, secret := range []string{"private-test", addr, "@ayi9.com", "raw_hex", "QAD", "community"} {
				if strings.Contains(output.String(), secret) {
					t.Fatal("输出泄漏非白名单信息")
				}
			}
		})
	}
	var out bytes.Buffer
	err = run(context.Background(), []string{"--profile", "et1k-ayi9-json", "--confirm-readonly"}, func(name string) string {
		if name == "UPS_WARDEN_PROBE_ADDRESS" {
			return aa
		}
		return "wrong-test"
	}, &out)
	if !errors.Is(err, ayi9.ErrPasswordWrong) || strings.Contains(err.Error(), "wrong-test") || out.Len() != 0 {
		t.Fatal("错误码或脱敏契约失败")
	}
}
