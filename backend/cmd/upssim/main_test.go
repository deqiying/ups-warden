package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type startedWriter struct{ ready chan string }

func (w startedWriter) Write(data []byte) (int, error) {
	w.ready <- string(data)
	return len(data), nil
}

func TestRunAndCancellation(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.yaml")
	registers := filepath.Join(dir, "registers.yaml")
	if err := os.WriteFile(scenario, []byte("schema: 1\nsteps: [{at: 30s, state: outage}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registers, []byte("schema: 1\nregisters: [{fc: 3, addr: 100, value: 2200}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"--scenario", scenario, "--registers", registers, "--community", "fake-community", "--password", "fake-password"}, startedWriter{ready}, io.Discard)
	}()
	var line string
	select {
	case line = <-ready:
	case err := <-done:
		t.Fatalf("启动失败: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("启动超时")
	}
	if strings.Contains(line, "fake-") {
		t.Fatal("启动输出泄露凭据")
	}
	var snmp, modbus, ayi9 string
	var et1k bool
	if _, err := fmt.Sscanf(line, "SNMP=%s Modbus=%s AYI9=%s ET1K=%t", &snmp, &modbus, &ayi9, &et1k); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", modbus, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未关闭模拟器")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("取消后连接未关闭")
	}
	for _, address := range []string{modbus, ayi9} {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatal("关闭后仍监听")
		}
	}
}

func TestRunInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"--snmp-port", "-1"}, {"--modbus-port", "65536"}, {"--ayi9-address", "0.0.0.0:0"}, {"--unknown"}, {"extra"}, {"--scenario", "missing-file"}, {"--snmp-port", "1234", "--snmp-address", "127.0.0.1:0"}} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := run(ctx, args, io.Discard, io.Discard); err == nil {
			t.Fatalf("接受无效参数: %v", args)
		}
	}
	var help bytes.Buffer
	if err := run(context.Background(), []string{"--help"}, io.Discard, &help); err != nil || !strings.Contains(help.String(), "scenario") {
		t.Fatal("帮助输出错误")
	}
	dir := t.TempDir()
	large := filepath.Join(dir, "large.yaml")
	if err := os.WriteFile(large, bytes.Repeat([]byte("x"), 65537), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(large); err == nil {
		t.Fatal("接受超大配置")
	}
	if _, err := readConfig(dir); err == nil {
		t.Fatal("接受目录")
	}
}
