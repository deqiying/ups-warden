package collector

import (
	"errors"

	"ups-warden/internal/driver/ayi9"
)

// ErrPasswordWrong 保留可供连接测试和运行状态识别的安全密码错误。
var ErrPasswordWrong = ayi9.ErrPasswordWrong

// 仅复制驱动已定义的安全错误码；包装链可能包含报文或凭据，不能原样传播。
func safeDriverError(err error) error {
	var device *ayi9.DeviceError
	if errors.As(err, &device) {
		return errors.Join(ErrDriver, &ayi9.DeviceError{Code: device.Code})
	}
	if errors.Is(err, ErrPasswordWrong) {
		return errors.Join(ErrDriver, ErrPasswordWrong)
	}
	return ErrDriver
}
