package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"ups-warden/internal/sim"
)

// maxDevices 覆盖现有 100 台压测规模，同时限制单个守护进程的监听器数量。
const maxDevices = 128

const devicesFile = "devices.json"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// apiError 是控制面的稳定错误；Message 为中文且不含凭据。
type apiError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return fmt.Sprintf("%s（%s）", e.Message, e.Code) }

func newAPIError(status int, code, format string, args ...any) *apiError {
	return &apiError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func validationError(format string, args ...any) *apiError {
	return newAPIError(http.StatusUnprocessableEntity, "validation_failed", format, args...)
}

// physical 是可选物理参数；nil 表示沿用默认值或当前值。
type physical struct {
	LoadPct          *float64 `json:"load_pct,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	InputVoltage     *float64 `json:"input_voltage,omitempty"`
	DischargeMinutes *float64 `json:"discharge_minutes,omitempty"`
}

// createRequest 是 POST /v1/devices 的请求体。端口 0 由系统分配。
type createRequest struct {
	Name          string    `json:"name"`
	ET1K          bool      `json:"et1k"`
	Phases        int       `json:"phases,omitempty"`
	IP            string    `json:"ip,omitempty"`
	SNMPPort      int       `json:"snmp_port,omitempty"`
	ModbusPort    int       `json:"modbus_port,omitempty"`
	AYI9Port      int       `json:"ayi9_port,omitempty"`
	Mode          *sim.Mode `json:"mode,omitempty"`
	Link          *string   `json:"link,omitempty"`
	Capacity      *float64  `json:"battery_capacity_pct,omitempty"`
	Params        physical  `json:"params"`
	ScenarioYAML  *string   `json:"scenario_yaml,omitempty"`
	Community     *string   `json:"community,omitempty"`
	Password      *string   `json:"password,omitempty"`
	AYI9ErrorCode int       `json:"ayi9_error_code,omitempty"`
}

// patchRequest 是 PATCH /v1/devices/{name} 的请求体；监听地址、ET1K、相数与凭据不可修改。
type patchRequest struct {
	Mode          *sim.Mode `json:"mode,omitempty"`
	Link          *string   `json:"link,omitempty"`
	Flash         bool      `json:"flash,omitempty"`
	Capacity      *float64  `json:"battery_capacity_pct,omitempty"`
	Params        physical  `json:"params"`
	AYI9ErrorCode *int      `json:"ayi9_error_code,omitempty"`
	ScenarioYAML  *string   `json:"scenario_yaml,omitempty"`
}

// record 是 devices.json 中的持久化定义。端口为实际分配值，重启后按原端口恢复。
// ScenarioYAML 在手动控制下仍作为物理参数基线（额定功率、电池电压等）。
// Capacity 是最近一次显式设置的电量，恢复手动设备时作为起始电量；nil 表示取基线默认值。
type record struct {
	Name          string     `json:"name"`
	CreatedAt     time.Time  `json:"created_at"`
	ET1K          bool       `json:"et1k"`
	Phases        int        `json:"phases"`
	IP            string     `json:"ip"`
	SNMPPort      int        `json:"snmp_port"`
	ModbusPort    int        `json:"modbus_port"`
	AYI9Port      int        `json:"ayi9_port"`
	Control       string     `json:"control"`
	Mode          sim.Mode   `json:"mode,omitempty"`
	Link          string     `json:"link,omitempty"`
	Capacity      *float64   `json:"battery_capacity_pct,omitempty"`
	Params        sim.Params `json:"params"`
	ScenarioYAML  string     `json:"scenario_yaml,omitempty"`
	Community     string     `json:"community"`
	Password      string     `json:"password"`
	AYI9ErrorCode int        `json:"ayi9_error_code"`
}

type devicesDocument struct {
	Schema  int      `json:"schema"`
	Devices []record `json:"devices"`
}

type device struct {
	rec     record
	server  *sim.Server
	failure string
}

// registry 管理守护进程内的全部模拟设备；所有修改串行执行并在成功后原子写入 devices.json。
type registry struct {
	dir     string
	mu      sync.Mutex
	devices map[string]*device
	logf    func(format string, args ...any)
}

func newRegistry(dir string, logf func(string, ...any)) *registry {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &registry{dir: dir, devices: map[string]*device{}, logf: logf}
}

// loadRecords 读取持久化文件；文件不存在视为空。格式错误时拒绝启动，不覆盖原文件。
func loadRecords(dir string) ([]record, error) {
	data, err := os.ReadFile(filepath.Join(dir, devicesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", devicesFile, err)
	}
	var doc devicesDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s 无法解析，请修复或改用新的状态目录: %w", devicesFile, err)
	}
	if doc.Schema != 1 {
		return nil, fmt.Errorf("%s 的 schema %d 不受支持", devicesFile, doc.Schema)
	}
	return doc.Devices, nil
}

// restore 按持久化记录逐台恢复；单台失败只标记 failed，不影响其他设备，也不改用其他端口。
func (r *registry) restore(records []record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		d := &device{rec: rec}
		server, err := startDevice(rec)
		if err != nil {
			d.failure = err.Message
			r.logf("恢复设备 %s 失败: %s", rec.Name, err.Message)
		} else {
			d.server = server
			r.logf("已恢复设备 %s", rec.Name)
		}
		r.devices[rec.Name] = d
	}
}

func (r *registry) persistLocked() error {
	doc := devicesDocument{Schema: 1, Devices: make([]record, 0, len(r.devices))}
	for _, name := range r.namesLocked() {
		doc.Devices = append(doc.Devices, r.devices[name].rec)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(r.dir, devicesFile), append(data, '\n'))
}

// writeFileAtomic 先写同目录临时文件再 rename，避免读到写了一半的文件；Unix 下权限为 0600。
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("写入 %s: %w", filepath.Base(path), err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return errors.Join(fmt.Errorf("写入 %s: %w", filepath.Base(path), err), os.Remove(name))
	}
	if err := os.Chmod(name, 0600); err != nil {
		return errors.Join(fmt.Errorf("设置 %s 权限: %w", filepath.Base(path), err), os.Remove(name))
	}
	if err := os.Rename(name, path); err != nil {
		return errors.Join(fmt.Errorf("替换 %s: %w", filepath.Base(path), err), os.Remove(name))
	}
	return nil
}

func (r *registry) namesLocked() []string {
	names := make([]string, 0, len(r.devices))
	for name := range r.devices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validFinite(v *float64) bool { return v == nil || !math.IsNaN(*v) && !math.IsInf(*v, 0) }

func parseLink(link *string) (*bool, *apiError) {
	if link == nil {
		return nil, nil
	}
	switch *link {
	case "up":
		v := true
		return &v, nil
	case "down":
		v := false
		return &v, nil
	}
	return nil, validationError("link 必须为 up 或 down")
}

// endpoint 是冲突检查的键；SNMP 走 UDP，Modbus 与 AYI9 走 TCP，同端口不同传输层可共存。
type endpoint struct {
	transport, ip string
	port          int
}

func endpointsOf(rec record) map[string]endpoint {
	return map[string]endpoint{
		"SNMP":   {"udp", rec.IP, rec.SNMPPort},
		"Modbus": {"tcp", rec.IP, rec.ModbusPort},
		"AYI9":   {"tcp", rec.IP, rec.AYI9Port},
	}
}

func (r *registry) create(req createRequest) (deviceView, *apiError) {
	rec, cfg, aerr := buildCreate(req)
	if aerr != nil {
		return deviceView{}, aerr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[rec.Name]; ok {
		return deviceView{}, newAPIError(http.StatusConflict, "device_exists", "设备 %s 已存在", rec.Name)
	}
	if len(r.devices) >= maxDevices {
		return deviceView{}, newAPIError(http.StatusConflict, "limit_exceeded", "设备数已达上限 %d", maxDevices)
	}
	// 只能发现与其他模拟设备的冲突；与本机其他程序的冲突以实际绑定结果为准，不做端口扫描。
	for proto, ep := range endpointsOf(rec) {
		if ep.port == 0 {
			continue
		}
		for _, name := range r.namesLocked() {
			for otherProto, other := range endpointsOf(r.devices[name].rec) {
				if other == ep {
					return deviceView{}, newAPIError(http.StatusConflict, "address_conflict",
						"%s %s/%s 已被设备 %s 的 %s 占用", proto, ep.transport, net.JoinHostPort(ep.ip, strconv.Itoa(ep.port)), name, otherProto)
				}
			}
		}
	}
	server, aerr := startConfig(cfg)
	if aerr != nil {
		return deviceView{}, aerr
	}
	snmp, modbus, ayi9 := server.Addresses()
	rec.SNMPPort, rec.ModbusPort, rec.AYI9Port = portOf(snmp), portOf(modbus), portOf(ayi9)
	syncRecord(&rec, server.Snapshot())
	d := &device{rec: rec, server: server}
	r.devices[rec.Name] = d
	if err := r.persistLocked(); err != nil {
		delete(r.devices, rec.Name)
		return deviceView{}, newAPIError(http.StatusInternalServerError, "persist_failed", "写入 %s 失败，设备未添加: %v", devicesFile, errors.Join(err, server.Close()))
	}
	r.logf("已添加设备 %s（SNMP=%s Modbus=%s AYI9=%s）", rec.Name, snmp, modbus, ayi9)
	return r.viewLocked(d), nil
}

func portOf(address string) int {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

// buildCreate 校验创建请求并生成持久化记录与模拟器配置。
func buildCreate(req createRequest) (record, sim.Config, *apiError) {
	if !nameRE.MatchString(req.Name) {
		return record{}, sim.Config{}, validationError("设备名必须匹配 ^[a-z0-9][a-z0-9-]{0,31}$")
	}
	if req.IP == "" {
		req.IP = "127.0.0.1"
	}
	ip := net.ParseIP(req.IP)
	if ip == nil || !ip.IsLoopback() {
		return record{}, sim.Config{}, validationError("ip 必须是 loopback IP（不解析主机名）")
	}
	for _, port := range []int{req.SNMPPort, req.ModbusPort, req.AYI9Port} {
		if port < 0 || port > 65535 {
			return record{}, sim.Config{}, validationError("端口必须在 0..65535 内")
		}
	}
	if req.ModbusPort != 0 && req.ModbusPort == req.AYI9Port {
		return record{}, sim.Config{}, validationError("Modbus 与 AYI9 同为 TCP，不能使用同一端口")
	}
	if req.Phases != 0 && req.Phases != 1 && req.Phases != 3 {
		return record{}, sim.Config{}, validationError("phases 必须为 1 或 3")
	}
	if !validFinite(req.Capacity) || !validFinite(req.Params.LoadPct) || !validFinite(req.Params.Temperature) ||
		!validFinite(req.Params.InputVoltage) || !validFinite(req.Params.DischargeMinutes) {
		return record{}, sim.Config{}, validationError("物理参数必须为有限数值")
	}
	rec := record{Name: req.Name, CreatedAt: time.Now().UTC().Truncate(time.Second), ET1K: req.ET1K, IP: ip.String(),
		SNMPPort: req.SNMPPort, ModbusPort: req.ModbusPort, AYI9Port: req.AYI9Port,
		Community: "sim-public", AYI9ErrorCode: req.AYI9ErrorCode, Capacity: req.Capacity}
	if req.Community != nil && *req.Community != "" {
		rec.Community = *req.Community
	}
	if req.Password != nil {
		rec.Password = *req.Password
	}
	manual := req.Mode != nil || req.Link != nil || req.Capacity != nil || req.Params != (physical{})
	if req.ScenarioYAML != nil {
		if manual {
			return record{}, sim.Config{}, validationError("scenario 与 mode、link、电量及物理参数互斥")
		}
		rec.ScenarioYAML = *req.ScenarioYAML
		rec.Control = sim.ControlScenario
	} else {
		rec.Control = sim.ControlManual
		rec.Mode = sim.ModeMains
		if req.Mode != nil {
			rec.Mode = *req.Mode
		}
		rec.Link = "up"
		if req.Link != nil {
			rec.Link = *req.Link
		}
		rec.Params = sim.Params{}
		for _, p := range []struct {
			src *float64
			dst *float64
		}{{req.Params.LoadPct, &rec.Params.LoadPct}, {req.Params.Temperature, &rec.Params.Temperature},
			{req.Params.InputVoltage, &rec.Params.InputVoltage}, {req.Params.DischargeMinutes, &rec.Params.DischargeMinutes}} {
			if p.src != nil {
				if *p.src == 0 {
					return record{}, sim.Config{}, validationError("物理参数不能为 0（0 保留为默认值标记）")
				}
				*p.dst = *p.src
			}
		}
	}
	cfg, aerr := configOf(rec)
	if aerr != nil {
		return record{}, sim.Config{}, aerr
	}
	if req.Phases != 0 && req.Phases != cfg.Scenario.Phases {
		return record{}, sim.Config{}, validationError("phases 与场景文件中的 phases 不一致")
	}
	rec.Phases = cfg.Scenario.Phases
	if rec.Phases == 0 {
		rec.Phases = 1
	}
	return rec, cfg, nil
}

// configOf 由持久化记录生成模拟器配置；手动控制的状态通过 Initial 在开始监听前生效。
func configOf(rec record) (sim.Config, *apiError) {
	scenario := sim.Scenario{Phases: rec.Phases}
	if rec.ScenarioYAML != "" {
		parsed, err := sim.ParseScenario([]byte(rec.ScenarioYAML))
		if err != nil {
			return sim.Config{}, validationError("场景: %v", err)
		}
		if rec.Phases != 0 && parsed.Phases != rec.Phases {
			return sim.Config{}, validationError("场景 phases 与设备 phases 不一致")
		}
		scenario = parsed
	}
	if rec.Phases == 0 && scenario.Phases == 0 {
		scenario.Phases = 1
	}
	cfg := sim.Config{
		SNMPAddress:   net.JoinHostPort(rec.IP, strconv.Itoa(rec.SNMPPort)),
		ModbusAddress: net.JoinHostPort(rec.IP, strconv.Itoa(rec.ModbusPort)),
		AYI9Address:   net.JoinHostPort(rec.IP, strconv.Itoa(rec.AYI9Port)),
		Community:     rec.Community, Password: rec.Password, ET1K: rec.ET1K,
		Scenario: scenario, AYI9ErrorCode: rec.AYI9ErrorCode,
	}
	if rec.Control == sim.ControlManual {
		online, aerr := parseLink(&rec.Link)
		if aerr != nil {
			return sim.Config{}, aerr
		}
		mode := rec.Mode
		change := &sim.Change{Mode: &mode, Online: online, BatteryCapacity: rec.Capacity}
		for _, p := range []struct {
			value float64
			dst   **float64
		}{{rec.Params.LoadPct, &change.LoadPct}, {rec.Params.Temperature, &change.Temperature},
			{rec.Params.InputVoltage, &change.InputVoltage}, {rec.Params.DischargeMinutes, &change.DischargeMinutes}} {
			if p.value != 0 {
				v := p.value
				*p.dst = &v
			}
		}
		cfg.Initial = change
	}
	return cfg, nil
}

func startDevice(rec record) (*sim.Server, *apiError) {
	cfg, aerr := configOf(rec)
	if aerr != nil {
		return nil, aerr
	}
	return startConfig(cfg)
}

// startConfig 区分绑定失败与参数错误；sim.Start 失败时已回收所有已创建的监听器。
func startConfig(cfg sim.Config) (*sim.Server, *apiError) {
	server, err := sim.Start(cfg)
	if err == nil {
		return server, nil
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return nil, newAPIError(http.StatusConflict, "address_in_use", "绑定失败，端口可能被其他进程占用或权限不足，请换端口或使用 0: %v", err)
	}
	return nil, validationError("%v", err)
}

// syncRecord 把运行期状态写回记录，使重启恢复与当前配置一致。
func syncRecord(rec *record, st sim.Status) {
	rec.Control = st.Control
	rec.AYI9ErrorCode = st.AYI9ErrorCode
	if st.Control == sim.ControlManual {
		rec.Mode = st.Mode
		rec.Link = linkOf(st.Online)
		rec.Params = st.Params
		// low 模式会把电量钳制到 15%；同步钳制保存的显式电量，否则重启恢复会被 low 的电量约束拒绝。
		if rec.Mode == sim.ModeLow && rec.Capacity != nil && *rec.Capacity > 15 {
			v := 15.0
			rec.Capacity = &v
		}
	} else {
		rec.Mode, rec.Link, rec.Params, rec.Capacity = "", "", sim.Params{}, nil
	}
}

func linkOf(online bool) string {
	if online {
		return "up"
	}
	return "down"
}

func (r *registry) update(name string, req patchRequest) (deviceView, *apiError) {
	change := sim.Change{Mode: req.Mode, Flash: req.Flash, BatteryCapacity: req.Capacity, AYI9ErrorCode: req.AYI9ErrorCode,
		LoadPct: req.Params.LoadPct, Temperature: req.Params.Temperature, InputVoltage: req.Params.InputVoltage,
		DischargeMinutes: req.Params.DischargeMinutes}
	online, aerr := parseLink(req.Link)
	if aerr != nil {
		return deviceView{}, aerr
	}
	change.Online = online
	if !validFinite(req.Capacity) || !validFinite(req.Params.LoadPct) || !validFinite(req.Params.Temperature) ||
		!validFinite(req.Params.InputVoltage) || !validFinite(req.Params.DischargeMinutes) {
		return deviceView{}, validationError("物理参数必须为有限数值")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[name]
	if !ok {
		return deviceView{}, newAPIError(http.StatusNotFound, "device_not_found", "设备 %s 不存在", name)
	}
	if d.server == nil {
		return deviceView{}, newAPIError(http.StatusConflict, "device_failed", "设备 %s 未能启动（%s），请 remove 后重新 add", name, d.failure)
	}
	rec := d.rec
	if req.ScenarioYAML != nil {
		scenario, err := sim.ParseScenario([]byte(*req.ScenarioYAML))
		if err != nil {
			return deviceView{}, validationError("场景: %v", err)
		}
		if scenario.Phases != rec.Phases {
			return deviceView{}, validationError("phases 不可修改：场景 phases 必须为 %d", rec.Phases)
		}
		change.Scenario = &scenario
		rec.ScenarioYAML = *req.ScenarioYAML
	}
	if err := d.server.Apply(change); err != nil {
		return deviceView{}, validationError("%v", err)
	}
	if req.Capacity != nil {
		rec.Capacity = req.Capacity
	}
	syncRecord(&rec, d.server.Snapshot())
	d.rec = rec
	if err := r.persistLocked(); err != nil {
		// 运行期状态无法可靠回滚；明确告知已生效但未持久化，重启后会恢复为上一次保存的配置。
		r.logf("设备 %s 修改已生效但持久化失败: %v", name, err)
		return deviceView{}, newAPIError(http.StatusInternalServerError, "persist_failed", "修改已生效但写入 %s 失败，重启后将恢复旧配置: %v", devicesFile, err)
	}
	r.logf("已修改设备 %s（control=%s mode=%s link=%s）", name, rec.Control, rec.Mode, rec.Link)
	return r.viewLocked(d), nil
}

func (r *registry) remove(name string) *apiError {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[name]
	if !ok {
		return newAPIError(http.StatusNotFound, "device_not_found", "设备 %s 不存在", name)
	}
	var closeErr error
	if d.server != nil {
		closeErr = d.server.Close()
	}
	delete(r.devices, name)
	if err := r.persistLocked(); err != nil {
		r.logf("设备 %s 已停止但持久化失败: %v", name, err)
		return newAPIError(http.StatusInternalServerError, "persist_failed", "设备已停止但写入 %s 失败，重启后会重新恢复: %v", devicesFile, err)
	}
	if closeErr != nil {
		r.logf("关闭设备 %s 的监听器: %v", name, closeErr)
	}
	r.logf("已删除设备 %s", name)
	return nil
}

func (r *registry) get(name string) (deviceView, *apiError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[name]
	if !ok {
		return deviceView{}, newAPIError(http.StatusNotFound, "device_not_found", "设备 %s 不存在", name)
	}
	return r.viewLocked(d), nil
}

func (r *registry) list() []deviceView {
	r.mu.Lock()
	defer r.mu.Unlock()
	views := make([]deviceView, 0, len(r.devices))
	for _, name := range r.namesLocked() {
		views = append(views, r.viewLocked(r.devices[name]))
	}
	return views
}

func (r *registry) counts() (total, failed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.devices {
		total++
		if d.server == nil {
			failed++
		}
	}
	return total, failed
}

// closeAll 关闭全部监听器但保留持久化记录，供下次启动恢复。
func (r *registry) closeAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, name := range r.namesLocked() {
		if d := r.devices[name]; d.server != nil {
			if err := d.server.Close(); err != nil {
				errs = append(errs, fmt.Errorf("关闭设备 %s: %w", name, err))
			}
			d.server = nil
		}
	}
	return errors.Join(errs...)
}

type endpointsView struct {
	SNMP   string `json:"snmp"`
	Modbus string `json:"modbus"`
	AYI9   string `json:"ayi9"`
}

type profilesView struct {
	SNMP   *string `json:"snmp"`
	Modbus *string `json:"modbus"`
	AYI9   *string `json:"ayi9"`
}

type snapshotView struct {
	Mains            bool    `json:"mains"`
	LowBattery       bool    `json:"low_battery"`
	BatteryCapacity  float64 `json:"battery_capacity_pct"`
	SecondsOnBattery int64   `json:"seconds_on_battery"`
	RuntimeSeconds   int64   `json:"runtime_seconds"`
}

// deviceView 是控制面返回的设备资源；不包含 community、password 或 token。
type deviceView struct {
	Name          string        `json:"name"`
	Status        string        `json:"status"`
	Failure       string        `json:"failure,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	ET1K          bool          `json:"et1k"`
	Phases        int           `json:"phases"`
	IP            string        `json:"ip"`
	Endpoints     endpointsView `json:"endpoints"`
	Profiles      profilesView  `json:"profiles"`
	Control       string        `json:"control"`
	Mode          sim.Mode      `json:"mode"`
	Link          string        `json:"link"`
	Flashing      bool          `json:"flashing"`
	Params        sim.Params    `json:"params"`
	AYI9ErrorCode int           `json:"ayi9_error_code"`
	PasswordSet   bool          `json:"password_set"`
	SharesIPWith  []string      `json:"shares_ip_with,omitempty"`
	Snapshot      *snapshotView `json:"snapshot,omitempty"`
}

// profilesFor 给出可直接用于创建数据源的内置 Profile 键；标准模式的 Modbus 没有通用内置 Profile。
func profilesFor(et1k bool) profilesView {
	s := func(v string) *string { return &v }
	if et1k {
		return profilesView{SNMP: s("et1k-snmp"), Modbus: s("et1k-modbus"), AYI9: s("et1k-ayi9-json")}
	}
	return profilesView{SNMP: s("rfc1628-generic"), AYI9: s("ayi9-json-generic")}
}

func (r *registry) viewLocked(d *device) deviceView {
	rec := d.rec
	join := func(port int) string { return net.JoinHostPort(rec.IP, strconv.Itoa(port)) }
	view := deviceView{Name: rec.Name, Status: "running", Failure: d.failure, CreatedAt: rec.CreatedAt, ET1K: rec.ET1K,
		Phases: rec.Phases, IP: rec.IP, Endpoints: endpointsView{join(rec.SNMPPort), join(rec.ModbusPort), join(rec.AYI9Port)},
		Profiles: profilesFor(rec.ET1K), Control: rec.Control, Mode: rec.Mode, Link: rec.Link, Params: rec.Params,
		AYI9ErrorCode: rec.AYI9ErrorCode, PasswordSet: rec.Password != ""}
	for _, name := range r.namesLocked() {
		if name != rec.Name && r.devices[name].rec.IP == rec.IP {
			view.SharesIPWith = append(view.SharesIPWith, name)
		}
	}
	if d.server == nil {
		view.Status = "failed"
		return view
	}
	st := d.server.Snapshot()
	view.Control, view.Mode, view.Link, view.Flashing, view.Params = st.Control, st.Mode, linkOf(st.Online), st.Flashing, st.Params
	view.Snapshot = &snapshotView{Mains: st.State.Mains, LowBattery: st.State.LowBattery,
		BatteryCapacity: math.Round(st.State.BatteryCapacity*10) / 10, SecondsOnBattery: st.State.SecondsOnBattery,
		RuntimeSeconds: st.State.RuntimeSeconds}
	return view
}
