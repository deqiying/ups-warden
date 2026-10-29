package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ups-warden/internal/sim"
)

func ptrTo[T any](v T) *T { return &v }

// freePort 找到同时在 UDP 与 TCP 上空闲的 loopback 端口；关闭后立即返回，存在极小的竞争窗口。
func freePort(t *testing.T) int {
	t.Helper()
	for range 20 {
		tl, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := tl.Addr().(*net.TCPAddr).Port
		ul, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		_ = tl.Close()
		if err == nil {
			_ = ul.Close()
			return port
		}
	}
	t.Fatal("找不到空闲端口")
	return 0
}

func newTestRegistry(t *testing.T) *registry {
	t.Helper()
	reg := newRegistry(t.TempDir(), t.Logf)
	t.Cleanup(func() {
		if err := reg.closeAll(); err != nil {
			t.Error(err)
		}
	})
	return reg
}

func TestCreateValidation(t *testing.T) {
	reg := newTestRegistry(t)
	for name, req := range map[string]createRequest{
		"非法名称":       {Name: "Bad_Name"},
		"非 loopback": {Name: "a", IP: "192.168.1.10"},
		"主机名":        {Name: "a", IP: "localhost"},
		"端口越界":       {Name: "a", SNMPPort: 70000},
		"TCP 同端口":    {Name: "a", ModbusPort: 15020, AYI9Port: 15020},
		"相数非法":       {Name: "a", Phases: 2},
		"ET1K 三相":    {Name: "a", ET1K: true, Phases: 3},
		"场景互斥":       {Name: "a", Mode: ptrTo(sim.ModeBattery), ScenarioYAML: ptrTo("schema: 1\n")},
		"低电大电量":      {Name: "a", Mode: ptrTo(sim.ModeLow), Capacity: ptrTo(50.0)},
		"零负载":        {Name: "a", Params: physical{LoadPct: ptrTo(0.0)}},
		"未知模式":       {Name: "a", Mode: ptrTo(sim.Mode("bypass"))},
		"未知通信":       {Name: "a", Link: ptrTo("flaky")},
	} {
		if _, aerr := reg.create(req); aerr == nil || aerr.Code != "validation_failed" {
			t.Fatalf("%s: 期望 validation_failed，得到 %v", name, aerr)
		}
	}
	if len(reg.list()) != 0 {
		t.Fatal("校验失败仍创建了设备")
	}
}

func TestEndpointConflicts(t *testing.T) {
	reg := newTestRegistry(t)
	port := freePort(t)
	if _, aerr := reg.create(createRequest{Name: "a", SNMPPort: port}); aerr != nil {
		t.Fatal(aerr)
	}
	if _, aerr := reg.create(createRequest{Name: "a"}); aerr == nil || aerr.Code != "device_exists" {
		t.Fatalf("重名应返回 device_exists: %v", aerr)
	}
	_, aerr := reg.create(createRequest{Name: "b", SNMPPort: port})
	if aerr == nil || aerr.Code != "address_conflict" || !strings.Contains(aerr.Message, "设备 a") {
		t.Fatalf("同 UDP 端口应冲突并指出占用者: %v", aerr)
	}
	// SNMP 走 UDP，Modbus 走 TCP：同 IP 同端口可共存。
	if _, aerr := reg.create(createRequest{Name: "b", ModbusPort: port}); aerr != nil {
		t.Fatalf("UDP 与 TCP 同端口应可共存: %v", aerr)
	}
	// 不同 loopback IP 不冲突。
	if _, aerr := reg.create(createRequest{Name: "c", IP: "127.0.0.2", SNMPPort: port}); aerr != nil {
		t.Fatalf("不同 IP 同端口应可共存: %v", aerr)
	}
	view, _ := reg.get("c")
	if len(view.SharesIPWith) != 0 {
		t.Fatal("独立 IP 不应提示共用")
	}
	if view, _ := reg.get("a"); len(view.SharesIPWith) != 1 || view.SharesIPWith[0] != "b" {
		t.Fatalf("共用 IP 提示错误: %v", view.SharesIPWith)
	}
}

func TestAddressInUseLeavesNoListener(t *testing.T) {
	reg := newTestRegistry(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	snmpPort := freePort(t)
	_, aerr := reg.create(createRequest{Name: "a", SNMPPort: snmpPort, ModbusPort: held.Addr().(*net.TCPAddr).Port})
	if aerr == nil || aerr.Code != "address_in_use" {
		t.Fatalf("期望 address_in_use: %v", aerr)
	}
	// SNMP 监听器先于 Modbus 创建，失败后必须已被回收。
	conn, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(snmpPort)))
	if err != nil {
		t.Fatalf("绑定失败后残留 SNMP 监听器: %v", err)
	}
	_ = conn.Close()
	if len(reg.list()) != 0 {
		t.Fatal("绑定失败仍登记了设备")
	}
}

func TestPersistAndRestore(t *testing.T) {
	dir := t.TempDir()
	reg := newRegistry(dir, t.Logf)
	a, aerr := reg.create(createRequest{Name: "a", ET1K: true, Mode: ptrTo(sim.ModeBattery), Capacity: ptrTo(60.0), Password: ptrTo("fake-pass")})
	if aerr != nil {
		t.Fatal(aerr)
	}
	b, aerr := reg.create(createRequest{Name: "b", IP: "127.0.0.2"})
	if aerr != nil {
		t.Fatal(aerr)
	}
	if _, aerr := reg.update("a", patchRequest{Mode: ptrTo(sim.ModeLow), Link: ptrTo("down"), Params: physical{LoadPct: ptrTo(55.0)}}); aerr != nil {
		t.Fatal(aerr)
	}
	if _, aerr := reg.update("a", patchRequest{Capacity: ptrTo(50.0)}); aerr == nil {
		t.Fatal("low 模式应拒绝大电量")
	}
	if err := reg.closeAll(); err != nil {
		t.Fatal(err)
	}
	// 端口 0 已持久化为实际端口；重启后按原端口恢复模式、通信与参数。
	records, err := loadRecords(dir)
	if err != nil || len(records) != 2 {
		t.Fatalf("读取持久化失败: %v %d", err, len(records))
	}
	// 占用 b 的 Modbus 端口，b 恢复失败但 a 正常。
	held, err := net.Listen("tcp", b.Endpoints.Modbus)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	restored := newRegistry(dir, t.Logf)
	restored.restore(records)
	t.Cleanup(func() { _ = restored.closeAll() })
	ra, _ := restored.get("a")
	if ra.Status != "running" || ra.Endpoints != a.Endpoints || ra.Mode != sim.ModeLow || ra.Link != "down" || ra.Params.LoadPct != 55 || !ra.PasswordSet {
		t.Fatalf("a 恢复错误: %+v", ra)
	}
	rb, _ := restored.get("b")
	if rb.Status != "failed" || rb.Failure == "" || rb.Endpoints != b.Endpoints {
		t.Fatalf("b 应标记 failed 且保留原端口: %+v", rb)
	}
	if _, aerr := restored.update("b", patchRequest{Mode: ptrTo(sim.ModeBattery)}); aerr == nil || aerr.Code != "device_failed" {
		t.Fatalf("failed 设备应拒绝修改: %v", aerr)
	}
	if total, failed := restored.counts(); total != 2 || failed != 1 {
		t.Fatalf("计数错误 %d/%d", total, failed)
	}
	if aerr := restored.remove("b"); aerr != nil {
		t.Fatal(aerr)
	}
	records, err = loadRecords(dir)
	if err != nil || len(records) != 1 || records[0].Name != "a" {
		t.Fatalf("删除未持久化: %v %+v", err, records)
	}
}

func TestLoadRecordsRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if records, err := loadRecords(dir); err != nil || records != nil {
		t.Fatal("文件不存在应视为空")
	}
	for _, content := range []string{"{not json", `{"schema": 2, "devices": []}`} {
		if err := os.WriteFile(filepath.Join(dir, devicesFile), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRecords(dir); err == nil {
			t.Fatalf("应拒绝 %q", content)
		}
	}
}

func TestScenarioDevice(t *testing.T) {
	reg := newTestRegistry(t)
	view, aerr := reg.create(createRequest{Name: "s", ScenarioYAML: ptrTo("schema: 1\nsteps: [{at: 0s, state: outage}]\n")})
	if aerr != nil {
		t.Fatal(aerr)
	}
	if view.Control != sim.ControlScenario || view.Mode != sim.ModeBattery {
		t.Fatalf("场景设备状态错误: %+v", view)
	}
	if _, aerr := reg.update("s", patchRequest{ScenarioYAML: ptrTo("schema: 1\nphases: 3\n")}); aerr == nil {
		t.Fatal("场景不能修改相数")
	}
	view, aerr = reg.update("s", patchRequest{Mode: ptrTo(sim.ModeMains)})
	if aerr != nil || view.Control != sim.ControlManual || view.Mode != sim.ModeMains {
		t.Fatalf("手动接管失败: %v %+v", aerr, view)
	}
}
