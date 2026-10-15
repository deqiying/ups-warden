package host

import (
	"context"
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// Linux 优先使用不需要 NET_RAW 的 ping socket，不可用时回退 raw socket。
func pingICMP(ctx context.Context, ip net.IP) (bool, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.IPPROTO_ICMP)
	if err != nil {
		return rawICMP(ctx, ip)
	}
	defer unix.Close(fd)
	packet, err := echoRequest()
	if err != nil {
		return false, errors.New("生成 ICMP 探测失败")
	}
	var address [4]byte
	copy(address[:], ip.To4())
	if err = unix.Sendto(fd, packet, 0, &unix.SockaddrInet4{Addr: address}); err != nil {
		return false, icmpPermission(err)
	}
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err = unix.Poll(poll, 25); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return false, icmpPermission(err)
		}
		if poll[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, from, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			return false, icmpPermission(err)
		}
		if a, ok := from.(*unix.SockaddrInet4); ok && a.Addr == address && echoReply(buf[:n], packet) {
			return true, nil
		}
	}
}
