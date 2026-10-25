// Package settings 读写 settings 表中的系统设置；每个设置一行，值为 JSON。
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	// 内嵌 IANA 时区库，保证容器与 Windows 上的时区校验结果一致。
	_ "time/tzdata"

	"ups-warden/internal/state"
)

// Settings 是全部系统设置。保留期以天为单位；DeviceDefaults 用于新建设备
// 及设备配置中省略的阈值字段；Timezone/Language 只供前端显示使用。
type Settings struct {
	DetailRetentionDays    int          `json:"detail_retention_days"`
	AggregateRetentionDays int          `json:"aggregate_retention_days"`
	LogRetentionDays       int          `json:"log_retention_days"`
	DeviceDefaults         state.Config `json:"device_defaults"`
	Timezone               string       `json:"timezone"`
	Language               string       `json:"language"`
	// HTTPActionGuard 对应 D8 护栏，键名与 action 包执行时读取的键一致。
	HTTPActionGuard bool `json:"http_action_guard_ups_hosts"`
}

// Defaults 返回未写入数据库时生效的设置。
func Defaults() Settings {
	return Settings{DetailRetentionDays: 7, AggregateRetentionDays: 90, LogRetentionDays: 365, DeviceDefaults: state.DefaultConfig(), Timezone: "UTC", Language: "zh-CN", HTTPActionGuard: true}
}

// Querier 由 *sql.DB 与 *sql.Tx 实现。
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Load 以默认值为底读取已保存的设置；未知键忽略，已知键的值无效时返回错误。
func Load(ctx context.Context, db Querier) (Settings, error) {
	s := Defaults()
	rows, err := db.QueryContext(ctx, "SELECT key,value FROM settings")
	if err != nil {
		return s, fmt.Errorf("读取系统设置: %w", err)
	}
	defer rows.Close()
	fields := s.fields()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return s, err
		}
		if key == "device_defaults" {
			if s.DeviceDefaults, err = state.ParseConfig([]byte(value)); err != nil {
				return s, fmt.Errorf("系统设置 %s 无效: %w", key, err)
			}
			continue
		}
		if p, ok := fields[key]; ok {
			if err := json.Unmarshal([]byte(value), p); err != nil || value == "null" {
				return s, fmt.Errorf("系统设置 %s 无效", key)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	if err := s.Validate(); err != nil {
		return s, fmt.Errorf("系统设置无效: %w", err)
	}
	return s, nil
}

func (s *Settings) fields() map[string]any {
	return map[string]any{
		"detail_retention_days":       &s.DetailRetentionDays,
		"aggregate_retention_days":    &s.AggregateRetentionDays,
		"log_retention_days":          &s.LogRetentionDays,
		"device_defaults":             &s.DeviceDefaults,
		"timezone":                    &s.Timezone,
		"language":                    &s.Language,
		"http_action_guard_ups_hosts": &s.HTTPActionGuard,
	}
}

// Validate 检查业务支持范围：明细 1～90 天，聚合与日志 1～3650 天，
// 聚合不短于明细；时区须为 IANA 名称；语言仅 zh-CN、en-US。
func (s Settings) Validate() error {
	if s.DetailRetentionDays < 1 || s.DetailRetentionDays > 90 || s.AggregateRetentionDays < s.DetailRetentionDays || s.AggregateRetentionDays > 3650 || s.LogRetentionDays < 1 || s.LogRetentionDays > 3650 {
		return errors.New("保留期超出范围")
	}
	if s.Timezone == "" || s.Timezone == "Local" {
		return errors.New("时区须为 IANA 名称")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return errors.New("时区须为 IANA 名称")
	}
	if s.Language != "zh-CN" && s.Language != "en-US" {
		return errors.New("不支持的语言")
	}
	return s.DeviceDefaults.Validate()
}

// Save 在调用方事务中写入全部设置。
func Save(ctx context.Context, tx *sql.Tx, s Settings) error {
	for key, p := range s.fields() {
		value, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value)); err != nil {
			return err
		}
	}
	return nil
}

// Days 把天数转换为时长。
func Days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
