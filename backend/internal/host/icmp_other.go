//go:build !linux

package host

import (
	"context"
	"net"
)

// 非 Linux 平台使用标准库 raw socket；权限不足时明确建议 TCP。
func pingICMP(ctx context.Context, ip net.IP) (bool, error) { return rawICMP(ctx, ip) }
