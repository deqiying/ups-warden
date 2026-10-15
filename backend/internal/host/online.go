package host

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strconv"
	"time"
)

// CheckOnline 使用指定端口或单次 ICMP echo；拒绝连接/探测超时表示离线，取消返回错误。
// 在线检测不发送 SSH 认证，也不要求确认指纹。
func CheckOnline(ctx context.Context, h Record) (bool, error) {
	if !validAddress(h.Address) {
		return false, errors.New("在线检测地址无效")
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var online bool
	var err error
	switch h.OnlineCheck.Type {
	case "tcp":
		if h.OnlineCheck.Port < 1 || h.OnlineCheck.Port > 65535 {
			return false, errors.New("在线检测 TCP 端口无效")
		}
		var conn net.Conn
		conn, err = (&net.Dialer{}).DialContext(probe, "tcp", net.JoinHostPort(h.Address, strconv.Itoa(h.OnlineCheck.Port)))
		if err == nil {
			_ = conn.Close()
			return true, nil
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		var dns *net.DNSError
		if errors.As(err, &dns) {
			return false, errors.New("在线检测无法解析主机地址")
		}
		return false, nil
	case "icmp":
		var ips []net.IP
		ips, err = net.DefaultResolver.LookupIP(probe, "ip4", h.Address)
		if err == nil && len(ips) > 0 {
			online, err = pingICMP(probe, ips[0])
		} else if err == nil {
			err = errors.New("ICMP 当前需要 IPv4 地址，请改用 TCP 在线检测")
		}
	default:
		return false, errors.New("在线检测类型无效")
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false, nil
	}
	return online, err
}

func echoRequest() ([]byte, error) {
	packet := make([]byte, 24)
	packet[0] = 8
	if _, err := rand.Read(packet[4:]); err != nil {
		return nil, err
	}
	var sum uint32
	for i := 0; i < len(packet); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(packet[i:]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(packet[2:], ^uint16(sum))
	return packet, nil
}
func echoReply(packet, request []byte) bool {
	// raw socket 在部分平台保留 IPv4 头；只接受本次随机 payload 的 echo reply。
	if len(packet) > 20 && packet[0]>>4 == 4 {
		n := int(packet[0]&15) * 4
		if n < 20 || n > len(packet) {
			return false
		}
		packet = packet[n:]
	}
	return len(packet) >= len(request) && packet[0] == 0 && packet[1] == 0 && bytes.Equal(packet[8:len(request)], request[8:])
}
func icmpPermission(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return errors.New("ICMP 权限不足：请改用 TCP 在线检测，或配置 ping_group_range / NET_RAW / 管理员权限")
	}
	return errors.New("ICMP socket 不可用，请检查网络配置或改用 TCP 在线检测")
}
func rawICMP(ctx context.Context, ip net.IP) (bool, error) {
	packet, err := echoRequest()
	if err != nil {
		return false, errors.New("生成 ICMP 探测失败")
	}
	conn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return false, icmpPermission(err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return false, errors.New("ICMP 设置超时失败")
		}
	}
	if _, err = conn.WriteTo(packet, &net.IPAddr{IP: ip}); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, icmpPermission(err)
	}
	buf := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return false, nil
			}
			return false, icmpPermission(err)
		}
		from, ok := addr.(*net.IPAddr)
		if ok && from.IP.Equal(ip) && echoReply(buf[:n], packet) {
			return true, nil
		}
	}
}
