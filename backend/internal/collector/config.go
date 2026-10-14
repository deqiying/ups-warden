// Package collector 调度只读采集；原始诊断只保存在内存，不写业务存储。
package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/modbus"
	"ups-warden/internal/driver/snmp"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
)

// Params 仅允许协议连接参数；读取策略完全由 Profile 决定。
// Version 为 SNMP 的 1/2c（空值默认 2c）；UnitID 仅用于 Modbus，nil 使用 Profile。
type Params struct {
	Version string `json:"version,omitempty"`
	UnitID  *uint8 `json:"unit_id,omitempty"`
}

// Source 是单个数据源配置。Factory 收到的 Host 已固定为 canonical IP。
// Credential 是单个 community/password 字符串，不是 JSON；DB 密文 AAD 为 sources.secret。
type Source struct {
	ID, DeviceID          int64
	Protocol, Host        string
	Port                  int
	Profile               *profile.Profile
	PollInterval, Timeout time.Duration
	Retries               int
	Params                Params
	Credential            secret.String
}

var (
	ErrConfig        = errors.New("collector: invalid_config")
	ErrConflict      = errors.New("collector: multiple_enabled_sources")
	ErrClosed        = errors.New("collector: closed")
	ErrNotStarted    = errors.New("collector: not_started")
	ErrBusy          = errors.New("collector: busy")
	ErrNoObservation = errors.New("collector: no_observation")
	ErrDriver        = errors.New("collector: driver_failed")
	ErrPanic         = errors.New("collector: driver_panic")
	ErrStore         = errors.New("collector: store_failed")
)

func parseParams(text, protocol string) (Params, error) {
	var p Params
	// 逐键解析同时拒绝重复键、null、未知键和尾随内容。
	d := json.NewDecoder(bytes.NewBufferString(text))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return p, ErrConfig
	}
	seen := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return p, ErrConfig
		}
		k, ok := t.(string)
		if !ok || seen[k] {
			return p, ErrConfig
		}
		seen[k] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return p, ErrConfig
		}
		switch k {
		case "version":
			if protocol != driver.ProtocolSNMP || json.Unmarshal(raw, &p.Version) != nil {
				return p, ErrConfig
			}
		case "unit_id":
			if protocol != driver.ProtocolModbus || json.Unmarshal(raw, &p.UnitID) != nil {
				return p, ErrConfig
			}
		default:
			return p, ErrConfig
		}
	}
	if _, err = d.Token(); err != nil {
		return p, ErrConfig
	}
	if _, err = d.Token(); err != io.EOF {
		return p, ErrConfig
	}
	return p, nil
}

func validate(source Source) (driver.ReadPlan, error) {
	var empty driver.ReadPlan
	if source.Profile == nil || source.Protocol != source.Profile.Protocol || source.Host == "" || source.Port < 1 || source.Port > 65535 || source.Timeout <= 0 || source.Timeout > time.Minute || source.PollInterval <= 0 || source.Retries < 0 {
		return empty, ErrConfig
	}
	switch source.Protocol {
	case driver.ProtocolSNMP:
		if source.Params.UnitID != nil || (source.Params.Version != "" && source.Params.Version != "1" && source.Params.Version != "2c") {
			return empty, ErrConfig
		}
	case driver.ProtocolModbus:
		if source.Params.Version != "" {
			return empty, ErrConfig
		}
	case driver.ProtocolAYI9:
		if source.Params.Version != "" || source.Params.UnitID != nil {
			return empty, ErrConfig
		}
	default:
		return empty, ErrConfig
	}
	plan, err := source.Profile.ReadPlan()
	if err != nil {
		return empty, ErrConfig
	}
	if plan.Modbus != nil && source.Params.UnitID != nil {
		plan.Modbus.UnitID = *source.Params.UnitID
	}
	return plan, nil
}

func defaultFactory(source Source, plan driver.ReadPlan) (driver.Driver, error) {
	cfg := guard.Config{Address: net.JoinHostPort(source.Host, strconv.Itoa(source.Port)), Timeout: source.Timeout}
	switch source.Protocol {
	case driver.ProtocolSNMP:
		return snmp.New(snmp.Config{Connection: cfg, Version: source.Params.Version, Community: source.Credential}, *plan.SNMP)
	case driver.ProtocolModbus:
		return modbus.New(modbus.Config{Connection: cfg}, *plan.Modbus)
	case driver.ProtocolAYI9:
		return ayi9.New(ayi9.Config{Connection: cfg, Password: source.Credential}, *plan.AYI9)
	}
	return nil, ErrConfig
}

func (s *Supervisor) canonical(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	ips, err := s.opts.Resolve(ctx, host)
	if err != nil || len(ips) == 0 {
		return "", ErrConfig
	}
	result := ""
	for _, addr := range ips {
		// 多地址 DNS 不做轮询或故障转移，避免与固定 IP 数据源绕过串行约束。
		if addr.Zone != "" || addr.IP == nil {
			return "", ErrConfig
		}
		canonical := addr.IP.String()
		if result != "" && canonical != result {
			return "", ErrConfig
		}
		result = canonical
	}
	return result, nil
}

func (s *Supervisor) load(ctx context.Context) ([]Source, error) {
	if s.db == nil {
		return nil, ErrStore
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT s.id,s.device_id,s.protocol,s.host,s.port,s.params,s.secret,s.profile_key,s.profile_version,s.poll_ms,s.timeout_ms,s.retries FROM sources s JOIN devices d ON d.id=s.device_id WHERE s.enabled=1 AND d.enabled=1 ORDER BY s.id`)
	if err != nil {
		return nil, ErrStore
	}
	var sources []Source
	devices := map[int64]bool{}
	// 先释放 rows 再查 profiles，避免占用连接池或嵌套查询。
	type reference struct {
		key     string
		version int
	}
	var refs []reference
	for rows.Next() {
		var source Source
		var params string
		var encrypted []byte
		var ref reference
		var poll, timeout int64
		if rows.Scan(&source.ID, &source.DeviceID, &source.Protocol, &source.Host, &source.Port, &params, &encrypted, &ref.key, &ref.version, &poll, &timeout, &source.Retries) != nil {
			_ = rows.Close()
			return nil, ErrStore
		}
		if devices[source.DeviceID] {
			_ = rows.Close()
			return nil, ErrConflict
		}
		devices[source.DeviceID] = true
		// 限制转换前的值，防止 duration 乘法溢出。
		if poll <= 0 || poll > int64((1<<63-1)/int64(time.Millisecond)) || timeout <= 0 || timeout > 60000 {
			_ = rows.Close()
			return nil, ErrConfig
		}
		source.PollInterval, source.Timeout = time.Duration(poll)*time.Millisecond, time.Duration(timeout)*time.Millisecond
		source.Params, err = parseParams(params, source.Protocol)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(encrypted) != 0 {
			if s.cipher == nil {
				_ = rows.Close()
				return nil, ErrConfig
			}
			plain, e := s.cipher.Decrypt(encrypted, "sources.secret")
			if e != nil {
				_ = rows.Close()
				return nil, ErrConfig
			}
			source.Credential = secret.NewString(string(plain))
		}
		sources = append(sources, source)
		refs = append(refs, ref)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, ErrStore
	}
	for i := range sources {
		ref := refs[i]
		var p *profile.Profile
		if profile.IsBuiltinKey(ref.key) {
			p, err = profile.Builtin(ref.key)
		} else {
			var content, protocol string
			var version int
			err = s.db.Read().QueryRowContext(ctx, `SELECT content,protocol,version FROM profiles WHERE key=?`, ref.key).Scan(&content, &protocol, &version)
			if err == nil {
				p, err = profile.Parse([]byte(content))
				if err == nil && (p.Key != ref.key || p.Protocol != protocol || p.Version != version) {
					err = ErrConfig
				}
			}
		}
		if err != nil || p == nil || p.Version != ref.version {
			return nil, ErrConfig
		}
		sources[i].Profile = p
		if _, err := validate(sources[i]); err != nil {
			return nil, err
		}
	}
	return sources, nil
}
