package sim

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"ups-warden/internal/driver"
)

const maxPacket = 32768
const ioTimeout = 2 * time.Second
const maxRequests = 4096

// Config 指定三个本机监听器和虚构测试凭据。空地址采用 127.0.0.1:0。
// Now 可注入虚拟时钟；调用者须保证并发安全。Registers 非 nil 时替换默认寄存器表。
type Config struct {
	SNMPAddress, ModbusAddress, AYI9Address string
	Community                               string
	Password                                string
	ET1K                                    bool
	Scenario                                Scenario
	Registers                               map[driver.RegKey]uint16
	AYI9ErrorCode                           int
	Now                                     func() time.Time
}

// Request 仅保留安全元信息。Data 是重新编码且清除凭据后的完整合法读请求，
// 仍可交给 driver guard 校验；非法/无法解析请求的 Data 为 nil，Allowed 为 false。
// 不保留原始 community、pass、session 或任意未知 JSON 字段。
type Request struct {
	Protocol string
	Data     []byte
	Allowed  bool
	Size     int
	At       time.Time
}

// Server 同时提供三种协议，共享场景时钟；最多 64 条 TCP 连接及 4096 条请求记录。
// 记录达到上限后丢弃新记录并递增 DroppedRequests，避免无限增长。
type Server struct {
	cfg          Config
	started      time.Time
	udp          *net.UDPConn
	modbus, ayi9 net.Listener
	mu           sync.Mutex
	conns        map[net.Conn]struct{}
	requests     []Request
	dropped      uint64
	closed       bool
	wg           sync.WaitGroup
	closeOnce    sync.Once
	closeErr     error
}

// Start 校验场景、寄存器与监听地址后启动三个本机监听器；失败时回收已分配资源。
func Start(cfg Config) (*Server, error) {
	var err error
	cfg.Scenario, err = cfg.Scenario.normalized()
	if err != nil {
		return nil, err
	}
	if cfg.ET1K && cfg.Scenario.Phases != 1 {
		return nil, fmt.Errorf("ET1K 模式只支持单相；三相使用标准模式")
	}
	if len(cfg.Community) > 255 || len(cfg.Password) > 15 || cfg.AYI9ErrorCode < 0 || cfg.AYI9ErrorCode > 999 {
		return nil, fmt.Errorf("模拟器凭据长度或错误码超出限制")
	}
	if cfg.Community == "" {
		cfg.Community = "sim-public"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if len(cfg.Registers) > 4096 {
		return nil, fmt.Errorf("寄存器表超过 4096 项")
	}
	if cfg.Registers != nil {
		copyRegs := make(map[driver.RegKey]uint16, len(cfg.Registers))
		for k, v := range cfg.Registers {
			if k.FC != driver.FC03 && k.FC != driver.FC04 {
				return nil, fmt.Errorf("寄存器表仅允许 FC03/FC04")
			}
			if cfg.ET1K && (k.FC != driver.FC03 || k.Addr > 79) {
				return nil, fmt.Errorf("ET1K 寄存器只允许 FC03 0..79")
			}
			copyRegs[k] = v
		}
		cfg.Registers = copyRegs
	}
	addresses := []*string{&cfg.SNMPAddress, &cfg.ModbusAddress, &cfg.AYI9Address}
	for _, p := range addresses {
		if *p == "" {
			*p = "127.0.0.1:0"
		}
		if err := loopbackAddress(*p); err != nil {
			return nil, err
		}
	}
	s := &Server{cfg: cfg, started: cfg.Now(), conns: make(map[net.Conn]struct{})}
	ua, err := net.ResolveUDPAddr("udp", cfg.SNMPAddress)
	if err != nil {
		return nil, fmt.Errorf("SNMP 地址: %w", err)
	}
	s.udp, err = net.ListenUDP("udp", ua)
	if err != nil {
		return nil, fmt.Errorf("SNMP 监听: %w", err)
	}
	s.modbus, err = net.Listen("tcp", cfg.ModbusAddress)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("Modbus 监听: %w", err), s.Close())
	}
	s.ayi9, err = net.Listen("tcp", cfg.AYI9Address)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("AYI9 监听: %w", err), s.Close())
	}
	s.wg.Add(3)
	go func() { defer s.wg.Done(); s.serveSNMP() }()
	go func() { defer s.wg.Done(); s.accept(s.modbus, s.serveModbus) }()
	go func() { defer s.wg.Done(); s.accept(s.ayi9, s.serveAYI9) }()
	return s, nil
}

func loopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("监听地址必须为本机 IP:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("监听地址只允许 loopback IP，不解析主机名")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("监听端口必须在 0..65535 内")
	}
	return nil
}

// Addresses 返回实际分配的 SNMP UDP、Modbus TCP 和 AYI9 TCP 地址。
func (s *Server) Addresses() (snmp, modbus, ayi9 string) {
	return s.udp.LocalAddr().String(), s.modbus.Addr().String(), s.ayi9.Addr().String()
}

// Close 幂等关闭所有监听器和活跃连接，并等待所有服务 goroutine 退出。
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for c := range s.conns {
			if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}
		s.mu.Unlock()
		for _, closer := range []interface{ Close() error }{s.udp, s.modbus, s.ayi9} {
			// 接口中的 typed nil 需显式跳过，供 Start 部分失败时清理。
			switch c := closer.(type) {
			case *net.UDPConn:
				if c == nil {
					continue
				}
			case nil:
				continue
			}
			if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}
		s.wg.Wait()
	})
	return s.closeErr
}

// Requests 返回记录的深拷贝，调用者修改不会影响服务端。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Request(nil), s.requests...)
	for i := range out {
		out[i].Data = append([]byte(nil), out[i].Data...)
	}
	return out
}

// DroppedRequests 返回超过记录上限后未保存的请求数量；只读断言应检查该值为零。
func (s *Server) DroppedRequests() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.dropped }

func (s *Server) record(protocol string, size int, data []byte, allowed bool) {
	request := Request{Protocol: protocol, Size: size, Data: append([]byte(nil), data...), Allowed: allowed, At: s.cfg.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) >= maxRequests {
		s.dropped++
		return
	}
	s.requests = append(s.requests, request)
}
func (s *Server) state() State { return s.cfg.Scenario.StateAt(s.cfg.Now().Sub(s.started)) }
func (s *Server) accept(listener net.Listener, serve func(net.Conn)) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed || len(s.conns) >= 64 {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() { _ = conn.Close(); s.mu.Lock(); delete(s.conns, conn); s.mu.Unlock() }()
			serve(conn)
		}()
	}
}
