package model

import "time"

// NotificationResult 是不含凭据的单接收目标发送结果，也用于 notify_failed SSE。
type NotificationResult struct {
	ID               int64     `json:"id"`
	ChannelID        int64     `json:"channel_id"`
	EventID          *int64    `json:"event_id"`
	At               time.Time `json:"at"`
	Target           string    `json:"target"`
	Summary          string    `json:"summary"`
	OK               bool      `json:"ok"`
	DurationMS       int64     `json:"duration_ms"`
	Error            string    `json:"error"`
	Attempts         int       `json:"attempts"`
	TemplateFallback bool      `json:"template_fallback"`
}
