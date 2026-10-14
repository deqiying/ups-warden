package profile

import (
	"embed"
	"fmt"
)

//go:embed builtin/*.yaml
var builtinFiles embed.FS

var builtinKeys = []string{"rfc1628-generic", "ayi9-json-generic", "et1k-snmp", "et1k-modbus", "et1k-ayi9-json"}

// IsBuiltinKey 判断内置保留键，不读取设备或用户文件。
func IsBuiltinKey(key string) bool {
	for _, k := range builtinKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Builtin 每次解析内嵌 YAML，返回完全独立的可变实例。
func Builtin(key string) (*Profile, error) {
	if !IsBuiltinKey(key) {
		return nil, fmt.Errorf("unknown builtin profile %q", key)
	}
	data, err := builtinFiles.ReadFile("builtin/" + key + ".yaml")
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("builtin %s: %w", key, err)
	}
	return p, nil
}

// Builtins 返回五个全新实例；调用者修改不会污染后续加载。
func Builtins() ([]*Profile, error) {
	out := make([]*Profile, 0, len(builtinKeys))
	for _, key := range builtinKeys {
		p, err := Builtin(key)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
