package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"ups-warden/internal/settings"
	"ups-warden/internal/state"
)

type settingsInput struct {
	DetailRetentionDays    *int            `json:"detail_retention_days"`
	AggregateRetentionDays *int            `json:"aggregate_retention_days"`
	LogRetentionDays       *int            `json:"log_retention_days"`
	DeviceDefaults         json.RawMessage `json:"device_defaults"`
	Timezone               *string         `json:"timezone"`
	Language               *string         `json:"language"`
	HTTPActionGuard        *bool           `json:"http_action_guard_ups_hosts"`
	// ConfirmDisableGuard 仅在把 HTTP 护栏从开启改为关闭时要求为 true（D8）。
	ConfirmDisableGuard bool `json:"confirm_disable_guard"`
}

func (s *Server) settingsRoutes() {
	s.mux.HandleFunc("GET /api/v1/settings", s.getSettings)
	s.mux.HandleFunc("PATCH /api/v1/settings", s.updateSettings)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	current, err := settings.Load(r.Context(), s.opts.DB.Read())
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, current)
}

// updateSettings 只覆盖显式字段；device_defaults 为整体替换，省略子字段取内置默认值。
// 保存与审计同事务，提交后保留期立即作用于历史聚合、清理和查询。
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := settings.Load(r.Context(), s.opts.DB.Read())
	if err != nil {
		s.internal(w, err)
		return
	}
	next := old
	if in.DetailRetentionDays != nil {
		next.DetailRetentionDays = *in.DetailRetentionDays
	}
	if in.AggregateRetentionDays != nil {
		next.AggregateRetentionDays = *in.AggregateRetentionDays
	}
	if in.LogRetentionDays != nil {
		next.LogRetentionDays = *in.LogRetentionDays
	}
	if in.DeviceDefaults != nil {
		if next.DeviceDefaults, err = state.ParseConfig(in.DeviceDefaults); err != nil {
			fail(w, 422, "validation_failed", "设备默认阈值无效")
			return
		}
	}
	if in.Timezone != nil {
		next.Timezone = *in.Timezone
	}
	if in.Language != nil {
		next.Language = *in.Language
	}
	if in.HTTPActionGuard != nil {
		next.HTTPActionGuard = *in.HTTPActionGuard
	}
	if err = next.Validate(); err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	if old.HTTPActionGuard && !next.HTTPActionGuard && !in.ConfirmDisableGuard {
		fail(w, 422, "confirmation_required", "关闭 HTTP 动作护栏需要 confirm_disable_guard=true")
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		if err := settings.Save(r.Context(), tx, next); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "settings.update", "settings", old, next)
	})
	if err != nil {
		s.internal(w, err)
		return
	}
	s.opts.History.SetRetention(settings.Days(next.DetailRetentionDays), settings.Days(next.AggregateRetentionDays), settings.Days(next.LogRetentionDays))
	reply(w, 200, next)
}

// deviceDefaults 返回系统设置中的设备默认阈值，供新建设备和省略字段使用。
func (s *Server) deviceDefaults(r *http.Request) (state.Config, error) {
	current, err := settings.Load(r.Context(), s.opts.DB.Read())
	return current.DeviceDefaults, err
}
