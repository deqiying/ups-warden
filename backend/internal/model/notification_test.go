package model

import "testing"

func TestNotificationUpdateCloneIsolation(t *testing.T) {
	id := int64(7)
	original := DeviceUpdate{NotifyFailed: &NotificationResult{ID: 1, EventID: &id, Summary: "safe"}}
	clone := CloneUpdate(original)
	clone.NotifyFailed.Summary = "changed"
	*clone.NotifyFailed.EventID = 9
	if original.NotifyFailed.Summary != "safe" || *original.NotifyFailed.EventID != 7 {
		t.Fatal("通知结果副本共享可变数据")
	}
}
