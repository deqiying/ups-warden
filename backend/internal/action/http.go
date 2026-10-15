package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ups-warden/internal/notify"
)

// httpTargets 每次保存、执行均读取设置和所有数据源（包括停用的），按 IP 比较。
// 返回本次解析结果供 DialContext 使用，不让校验后第二次 DNS 查询造成重绑定。
func (s *Service) httpTargets(ctx context.Context, rawURL string) ([]net.IPAddr, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("action: HTTP URL 无效")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, errors.New("action: HTTP 目标解析失败")
	}
	guard := true
	var value string
	err = s.db.Read().QueryRowContext(ctx, "SELECT value FROM settings WHERE key='http_action_guard_ups_hosts'").Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(value), &guard); err != nil || value == "null" {
			return nil, errors.New("action: HTTP 护栏设置无效")
		}
	}
	if !guard {
		return ips, nil
	}
	rows, err := s.db.Read().QueryContext(ctx, "SELECT DISTINCT host FROM sources")
	if err != nil {
		return nil, err
	}
	var hosts []string
	for rows.Next() {
		var h string
		if err = rows.Scan(&h); err != nil {
			break
		}
		hosts = append(hosts, h)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		if strings.EqualFold(strings.TrimSuffix(h, "."), strings.TrimSuffix(u.Hostname(), ".")) {
			return nil, errors.New("action: HTTP 目标与 UPS 数据源主机相同")
		}
		sourceIPs, e := net.DefaultResolver.LookupIPAddr(ctx, h)
		if e != nil || len(sourceIPs) == 0 {
			return nil, errors.New("action: UPS 数据源解析失败，拒绝 HTTP 动作")
		}
		for _, a := range ips {
			for _, b := range sourceIPs {
				if a.IP.Equal(b.IP) {
					return nil, errors.New("action: HTTP 目标与 UPS 数据源 IP 相同")
				}
			}
		}
	}
	return ips, nil
}
func renderBody(r Record, req Request) (string, error) {
	if r.Credentials.BodyTemplate.Reveal() == "" {
		return "", nil
	}
	return notify.Render(r.Credentials.BodyTemplate.Reveal(), "json", notify.Variables(req.DeviceName, req.Event, req.State, time.Now()))
}
func (s *Service) executeHTTP(ctx context.Context, r Record, req Request) result {
	out := result{status: "failed"}
	body, err := renderBody(r, req)
	if err != nil {
		out.message = "HTTP JSON 模板渲染失败"
		return out
	}
	ips, err := s.httpTargets(ctx, r.Credentials.URL.Reveal())
	if err != nil {
		out.message = "HTTP 目标解析或 UPS 主机护栏检查失败"
		return out
	}
	transport := &http.Transport{
		// Proxy=nil：不使用环境代理。URL 保持原 hostname，TLS 仍校验该主机证书。
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				host := ip.IP.String()
				if ip.Zone != "" {
					host += "%" + ip.Zone
				}
				conn, e := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
				if e == nil {
					return conn, nil
				}
				last = e
			}
			return nil, last
		},
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 64 * 1024,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}
	request, err := http.NewRequestWithContext(ctx, r.Config.Method, r.Credentials.URL.Reveal(), strings.NewReader(body))
	if err != nil {
		out.message = "HTTP 请求构造失败"
		return out
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.Credentials.Headers {
		request.Header.Set(k, v.Reveal())
	}
	response, err := client.Do(request)
	if err != nil {
		out.message = "HTTP 请求失败（连接、TLS 或重定向被拒绝）"
		return out
	}
	defer response.Body.Close()
	// 不持久化响应正文，避免对端回显 URL/Header/Body 中的秘密。
	out.stdout = fmt.Sprintf("HTTP status %d", response.StatusCode)
	expected := response.StatusCode >= 200 && response.StatusCode < 300
	if len(r.Config.ExpectedStatus) > 0 {
		expected = false
		for _, status := range r.Config.ExpectedStatus {
			if response.StatusCode == status {
				expected = true
			}
		}
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		expected = false
	}
	if !expected {
		out.message = "HTTP 响应状态码不符合预期"
		return out
	}
	out.status = "succeeded"
	return out
}
