package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"ups-warden/internal/collector"
	"ups-warden/internal/driver"
	"ups-warden/internal/normalize"
)

func (s *Server) testSource(w http.ResponseWriter, r *http.Request) {
	var in sourceInput
	if !decode(w, r, &in) {
		return
	}
	v := defaultSource(0)
	if in.TimeoutMS == nil {
		v.TimeoutMS = 5000
	}
	if err := s.applySource(r.Context(), &v, in); err != nil {
		s.storageError(w, err, "source")
		return
	}
	s.probe(w, r, v)
}
func (s *Server) testSavedSource(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.readSource(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "source")
		return
	}
	s.probe(w, r, v)
}
func (s *Server) probe(w http.ResponseWriter, r *http.Request, v source) {
	cfg, err := s.sourceConfig(r.Context(), v)
	if err != nil {
		s.storageError(w, err, "source")
		return
	}
	if s.opts.Collector == nil {
		fail(w, 503, "collector_unavailable", "采集服务不可用")
		return
	}
	result, err := s.opts.Collector.Test(r.Context(), cfg)
	if err != nil {
		code := "probe_failed"
		status := 502
		if errors.Is(err, collector.ErrBusy) {
			code = "source_busy"
			status = 409
		}
		fail(w, status, code, "只读连接测试未成功，请检查配置或稍后重试")
		return
	}
	value := map[string]any{"latency_ms": result.Latency.Milliseconds(), "device_info": result.DeviceInfo, "snapshot": result.Snapshot, "raw": rawView(result.Raw), "trace": result.Trace, "recommendations": result.Recommendations}
	safe, err := redactDiagnostic(value, cfg.Credential.Reveal())
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, safe)
}
func (s *Server) raw(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	v, err := s.readSource(r.Context(), id)
	if err != nil {
		s.storageError(w, err, "source")
		return
	}
	if s.opts.Collector == nil {
		fail(w, 503, "collector_unavailable", "采集服务不可用")
		return
	}
	raw, trace, ok := s.opts.Collector.Diagnostics(id)
	if !ok {
		fail(w, 404, "diagnostic_unavailable", "暂无诊断缓存")
		return
	}
	cfg, err := s.sourceConfig(r.Context(), v)
	if err != nil {
		s.storageError(w, err, "source")
		return
	}
	safe, err := redactDiagnostic(map[string]any{"raw": rawView(raw), "trace": safeTrace(trace)}, cfg.Credential.Reveal())
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, safe)
}
func rawView(raw driver.RawData) map[string]any {
	regs := map[string]uint16{}
	for key, value := range raw.Modbus {
		regs[registerKey(uint8(key.FC), key.Addr)] = value
	}
	errors := []map[string]any{}
	for _, e := range raw.Errors {
		errors = append(errors, map[string]any{"oid": e.OID, "fc": e.FC, "start": e.Start, "count": e.Count, "command": e.Command, "kind": e.Kind})
	}
	return map[string]any{"protocol": raw.Protocol, "at": raw.At.UTC(), "snmp": raw.SNMP, "modbus": regs, "ayi9": raw.AYI9, "errors": errors}
}
func safeTrace(trace []normalize.Trace) []normalize.Trace { return trace }

// 在值层清洗回显凭据；Base64 必须先解码再替换，编码不保持子串关系。
func redactDiagnostic(value any, credential string) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if credential != "" {
				if decoded, err := base64.StdEncoding.DecodeString(x); err == nil && bytes.Contains(decoded, []byte(credential)) {
					return base64.StdEncoding.EncodeToString(bytes.ReplaceAll(decoded, []byte(credential), []byte("******")))
				}
				x = strings.ReplaceAll(x, credential, "******")
				x = strings.ReplaceAll(x, base64.StdEncoding.EncodeToString([]byte(credential)), "******")
			}
			return x
		case map[string]any:
			for k, value := range x {
				x[k] = walk(value)
			}
		case []any:
			for i, value := range x {
				x[i] = walk(value)
			}
		}
		return v
	}
	return walk(out), nil
}
