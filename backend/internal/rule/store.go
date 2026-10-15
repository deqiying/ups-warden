package rule

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"ups-warden/internal/store"
)

// Columns 用于规则查询的固定列顺序。
const Columns = "id,name,enabled,dry_run,scope,trigger,actions,recovery_actions,cooldown_s"

// Scan 从数据库加载并验证规则，错误不回显原始配置。
func Scan(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var scope, trigger, actions, recovery []byte
	if err := row.Scan(&r.ID, &r.Name, &r.Enabled, &r.DryRun, &scope, &trigger, &actions, &recovery, &r.CooldownS); err != nil {
		return r, err
	}
	for _, v := range []struct {
		data   []byte
		target any
	}{{scope, &r.Scope}, {trigger, &r.Trigger}, {actions, &r.Actions}, {recovery, &r.RecoveryActions}} {
		d := json.NewDecoder(bytes.NewReader(v.data))
		d.DisallowUnknownFields()
		if err := d.Decode(v.target); err != nil {
			return r, errors.New("持久化规则结构无效")
		}
	}
	return r, Validate(r)
}

// Read 返回单条规则，不缓存配置。
func Read(ctx context.Context, db *store.Store, id int64) (Record, error) {
	return Scan(db.Read().QueryRowContext(ctx, "SELECT "+Columns+" FROM rules WHERE id=?", id))
}

// Encode 生成四个 JSON 列；调用方在保存边界完成校验。
func Encode(r Record) (scope, trigger, actions, recovery string, err error) {
	out := []*string{&scope, &trigger, &actions, &recovery}
	for i, v := range []any{r.Scope, r.Trigger, r.Actions, r.RecoveryActions} {
		var data []byte
		data, err = json.Marshal(v)
		if err != nil {
			return
		}
		*out[i] = string(data)
	}
	return
}
func loadInstance(ctx context.Context, tx *sql.Tx, ruleID, deviceID int64) (instance, error) {
	var st instance
	var firedAt sql.NullInt64
	var data []byte
	err := tx.QueryRowContext(ctx, "SELECT fired,fired_at,since FROM rule_state WHERE rule_id=? AND device_id=?", ruleID, deviceID).Scan(&st.Fired, &firedAt, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err = json.Unmarshal(data, &st); err != nil {
		return st, errors.New("规则实例状态损坏")
	}
	if firedAt.Valid {
		t := time.UnixMilli(firedAt.Int64).UTC()
		st.FiredAt = &t
	}
	return st, nil
}
func saveInstance(ctx context.Context, tx *sql.Tx, ruleID, deviceID int64, st instance) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	var firedAt any
	if st.FiredAt != nil {
		firedAt = st.FiredAt.UnixMilli()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rule_state(rule_id,device_id,fired,fired_at,since) VALUES(?,?,?,?,?)
	ON CONFLICT(rule_id,device_id) DO UPDATE SET fired=excluded.fired,fired_at=excluded.fired_at,since=excluded.since`, ruleID, deviceID, st.Fired, firedAt, string(data))
	return err
}
