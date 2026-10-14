package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

type persistedFlags struct {
	Active    map[string]bool `json:"active"`
	Causes    map[string]bool `json:"causes,omitempty"`
	LostSince *time.Time      `json:"lost_since,omitempty"`
	HeldPower string          `json:"held_power,omitempty"`
}

func restore(ctx context.Context, db *store.Store, id int64, cfg Config) (*Engine, error) {
	e, err := NewEngine(id, cfg)
	if err != nil {
		return nil, err
	}
	var pending, battery, lastOK sql.NullInt64
	var cycle sql.NullInt64
	var flags string
	var updated int64
	err = db.Read().QueryRowContext(ctx, `SELECT power_state,power_state_raw,pending_since,on_battery_since,outage_cycle_id,comm_state,last_ok_at,alert_flags,updated_at FROM device_state WHERE device_id=?`, id).Scan(&e.view.PowerState, &e.view.PowerStateRaw, &pending, &battery, &cycle, &e.view.CommState, &lastOK, &flags, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return e, nil
	}
	if err != nil {
		return nil, fmt.Errorf("恢复设备状态: %w", err)
	}
	if !validPower(e.view.PowerState) || !validPower(e.view.PowerStateRaw) {
		return nil, errors.New("持久化供电状态无效")
	}
	if e.view.CommState != "ok" && e.view.CommState != "lost" && e.view.CommState != "unknown" {
		return nil, errors.New("持久化通信状态无效")
	}
	var metadata persistedFlags
	if err := json.Unmarshal([]byte(flags), &metadata); err != nil {
		return nil, errors.New("设备恢复元数据损坏")
	}
	e.lostSince = metadata.LostSince
	e.heldPower = metadata.HeldPower
	for key, active := range metadata.Causes {
		if active {
			e.causes[key] = true
		}
	}
	if !validPower(e.heldPower) {
		e.heldPower = e.view.PowerState
	}
	if lastOK.Valid {
		e.view.LastOKAt = time.UnixMilli(lastOK.Int64).UTC()
	}
	if battery.Valid {
		e.view.OnBatterySince = timePtr(time.UnixMilli(battery.Int64).UTC())
	}
	if cycle.Valid {
		v := cycle.Int64
		e.view.OutageCycleID = &v
	}
	// events 表为活动告警事实来源，不从可能过时的字段快照重新生成。
	rows, err := db.Read().QueryContext(ctx, "SELECT type FROM events WHERE device_id=? AND active=1", id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		if err = rows.Scan(&kind); err != nil {
			break
		}
		e.flags[kind] = true
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, fmt.Errorf("恢复活动告警: %w", err)
	}
	if e.view.CommState == "lost" {
		e.failures = cfg.Failures
		if e.lostSince == nil {
			e.lostSince = timePtr(time.UnixMilli(updated))
		}
	}
	// 重启不恢复未确认的连续性；已确认周期保留原始起点，恢复仍重新计满窗口。
	if e.view.OutageCycleID == nil && !e.flags[model.EventMainsLost] {
		e.view.OnBatterySince = nil
	}
	e.view.PendingSince = nil
	e.view.PowerStateRaw = "unknown"
	e.view.Fields = observationFields(nil)
	e.lastAdvance = time.Now()
	e.derived(e.lastAdvance)
	return e, nil
}
func validPower(s string) bool {
	switch s {
	case "online", "on_battery", "bypass", "fault", "shutdown", "unknown":
		return true
	}
	return false
}
func criticalChanged(a, b *Engine) bool {
	return a.view.PowerState != b.view.PowerState || a.view.PowerStateRaw != b.view.PowerStateRaw || a.view.CommState != b.view.CommState || !reflect.DeepEqual(a.view.PendingSince, b.view.PendingSince) || !reflect.DeepEqual(a.view.OnBatterySince, b.view.OnBatterySince) || !reflect.DeepEqual(a.flags, b.flags) || !reflect.DeepEqual(a.causes, b.causes) || !reflect.DeepEqual(a.lostSince, b.lostSince) || a.heldPower != b.heldPower
}
func timestamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().UnixMilli()
}
func save(ctx context.Context, db *store.Store, e *Engine, update *model.DeviceUpdate) error {
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for i := range update.Events {
			event := &update.Events[i]
			if event.Type == model.EventMainsLost && e.view.OutageCycleID == nil {
				started := event.At
				if e.view.OnBatterySince != nil {
					started = *e.view.OnBatterySince
				}
				result, err := tx.ExecContext(ctx, "INSERT INTO outage_cycles(device_id,started_at) VALUES(?,?)", e.view.DeviceID, started.UnixMilli())
				if err != nil {
					return err
				}
				id, err := result.LastInsertId()
				if err != nil {
					return err
				}
				e.view.OutageCycleID = &id
			}
			if event.Type == model.EventMainsRestored && e.view.OutageCycleID != nil {
				if _, err := tx.ExecContext(ctx, "UPDATE outage_cycles SET ended_at=MAX(started_at,?) WHERE id=? AND device_id=? AND ended_at IS NULL", event.At.UnixMilli(), *e.view.OutageCycleID, e.view.DeviceID); err != nil {
					return err
				}
				e.view.OutageCycleID = nil
			}
			if event.Clears != "" {
				if _, err := tx.ExecContext(ctx, "UPDATE events SET active=0,cleared_at=MAX(started_at,?) WHERE device_id=? AND type=? AND active=1", event.At.UnixMilli(), e.view.DeviceID, event.Clears); err != nil {
					return err
				}
			}
			if event.Type == "" {
				continue
			} // 无恢复类型的告警只关闭原事件，不制造额外业务事件。
			summary, err := json.Marshal(event.Summary)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, "INSERT INTO events(device_id,type,severity,started_at,active,summary) VALUES(?,?,?,?,?,?)", event.DeviceID, event.Type, string(event.Severity), event.At.UnixMilli(), event.Active, string(summary))
			if err != nil {
				return err
			}
			event.ID, err = result.LastInsertId()
			if err != nil {
				return err
			}
		}
		flags, err := json.Marshal(persistedFlags{Active: e.flags, Causes: e.causes, LostSince: e.lostSince, HeldPower: e.heldPower})
		if err != nil {
			return err
		}
		var lastOK any
		if !e.view.LastOKAt.IsZero() {
			lastOK = e.view.LastOKAt.UTC().UnixMilli()
		}
		var cycle any
		if e.view.OutageCycleID != nil {
			cycle = *e.view.OutageCycleID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO device_state(device_id,power_state,power_state_raw,pending_since,on_battery_since,outage_cycle_id,comm_state,last_ok_at,alert_flags,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(device_id) DO UPDATE SET power_state=excluded.power_state,power_state_raw=excluded.power_state_raw,pending_since=excluded.pending_since,on_battery_since=excluded.on_battery_since,outage_cycle_id=excluded.outage_cycle_id,comm_state=excluded.comm_state,last_ok_at=excluded.last_ok_at,alert_flags=excluded.alert_flags,updated_at=excluded.updated_at`, e.view.DeviceID, e.view.PowerState, e.view.PowerStateRaw, timestamp(e.view.PendingSince), timestamp(e.view.OnBatterySince), cycle, e.view.CommState, lastOK, string(flags), e.view.At.UnixMilli())
		return err
	})
	if err != nil {
		return fmt.Errorf("提交设备状态与事件: %w", err)
	}
	filtered := update.Events[:0]
	for _, event := range update.Events {
		if event.Type != "" {
			filtered = append(filtered, event)
		}
	}
	update.Events = filtered
	update.State = e.Snapshot()
	return nil
}
