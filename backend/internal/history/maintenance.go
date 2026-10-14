package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Aggregate 以明细为唯一依据，重算保留期内所有完整分钟，覆盖迟到样本和重启补算。
// 每个设备/分钟单独事务，内存只持有单分钟统计；保留期边缘的部分分钟
// 不重算，避免清理后以残缺明细覆盖已经完整的聚合。不需要新增迁移。
func (w *Writer) Aggregate(ctx context.Context, now time.Time) error {
	lower := now.Add(-w.opts.DetailRetention).UTC()
	start := lower.Truncate(time.Minute)
	if lower.After(start) {
		start = start.Add(time.Minute)
	}
	end := now.UTC().Truncate(time.Minute)
	// 使用明确的 UTC 时间桶表达式，也正确处理 Unix epoch 前时间。
	rows, err := w.db.Read().QueryContext(ctx, `SELECT DISTINCT device_id, ts-((ts%60000+60000)%60000) AS bucket FROM history_sample WHERE ts>=? AND ts<? ORDER BY device_id,bucket`, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return fmt.Errorf("history: aggregate buckets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var device, bucket int64
		if err := rows.Scan(&device, &bucket); err != nil {
			return err
		}
		err := w.db.WithTx(ctx, func(tx *sql.Tx) error {
			detail, err := tx.QueryContext(ctx, `SELECT data FROM history_sample WHERE device_id=? AND ts>=? AND ts<? ORDER BY ts`, device, bucket, bucket+60000)
			if err != nil {
				return err
			}
			fields := make(map[string]AggregateValue)
			for detail.Next() {
				var data string
				if err := detail.Scan(&data); err != nil {
					detail.Close()
					return err
				}
				values, err := decodeRaw(data)
				if err != nil {
					detail.Close()
					return fmt.Errorf("decode detail: %w", err)
				}
				mergeFields(fields, values)
			}
			readErr := detail.Err()
			closeErr := detail.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if len(fields) == 0 {
				return nil
			}
			data, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO history_1m(device_id,bucket,data) VALUES(?,?,?) ON CONFLICT(device_id,bucket) DO UPDATE SET data=excluded.data WHERE history_1m.data<>excluded.data`, device, bucket, string(data))
			return err
		})
		if err != nil {
			return fmt.Errorf("history: aggregate device %d minute %d: %w", device, bucket, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("history: aggregate rows: %w", err)
	}
	return nil
}

const cleanupBatch = 5000

type cleanupTarget struct {
	table, predicate string
	cutoff           int64
}

// Cleanup 每次 DELETE/事务最多 5000 行，沿用现有普通 rowid 表。
// 只清理已结束日志，保留活动事件、活动周期，以及仍被外键引用的事件/周期。
// 本方法不自动聚合；调用方应在删除明细前先 Aggregate。
func (w *Writer) Cleanup(ctx context.Context, now time.Time) error {
	detail := now.Add(-w.opts.DetailRetention).UnixMilli()
	aggregate := now.Add(-w.opts.AggregateRetention).UnixMilli()
	logs := now.Add(-w.opts.LogRetention).UnixMilli()
	targets := []cleanupTarget{
		{"history_sample", "ts < ?", detail},
		{"history_1m", "bucket < ?", aggregate},
		{"audit_logs", "at < ?", logs},
		{"rule_logs", "at < ?", logs},
		{"notify_logs", "at < ?", logs},
		{"action_runs", "ended_at < ? AND status NOT IN ('running','countdown','waiting')", logs},
		// plan_runs 的步骤没有 ON DELETE CASCADE，先分批删除过期已结束运行的步骤。
		{"plan_run_steps", "run_id IN (SELECT id FROM plan_runs WHERE ended_at < ? AND status NOT IN ('countdown','running'))", logs},
		{"plan_runs", "ended_at < ? AND status NOT IN ('countdown','running')", logs},
		{"events", "active=0 AND COALESCE(cleared_at,started_at) < ? AND NOT EXISTS (SELECT 1 FROM notify_logs WHERE event_id=events.id)", logs},
		{"outage_cycles", "ended_at < ? AND NOT EXISTS (SELECT 1 FROM device_state WHERE outage_cycle_id=outage_cycles.id) AND NOT EXISTS (SELECT 1 FROM plan_runs WHERE outage_cycle_id=outage_cycles.id)", logs},
	}
	for _, target := range targets {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var count int64
			err := w.db.WithTx(ctx, func(tx *sql.Tx) error {
				// 仅使用内部常量构造 SQL；旧迁移表具有 rowid，不假设 WITHOUT ROWID。
				result, err := tx.ExecContext(ctx, "DELETE FROM "+target.table+" WHERE rowid IN (SELECT rowid FROM "+target.table+" WHERE "+target.predicate+" LIMIT 5000)", target.cutoff)
				if err != nil {
					return err
				}
				count, err = result.RowsAffected()
				return err
			})
			if err != nil {
				return fmt.Errorf("history: cleanup %s: %w", target.table, err)
			}
			if count < cleanupBatch {
				break
			}
		}
	}
	return nil
}
