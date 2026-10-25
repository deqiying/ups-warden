package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// AggregateValue 保存单字段统计量；降采样按 Count 加权而非平均各分钟的 Avg。
type AggregateValue struct {
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Avg   float64 `json:"avg"`
	Count int64   `json:"count"`
}

// Point 的 At 是 UTC 样本时间或时间桶起点。
type Point struct {
	At     time.Time                 `json:"at"`
	Fields map[string]AggregateValue `json:"fields"`
}

// Query 使用 [From,To)；Resolution 为 auto/raw/1m，空值等同 auto。
// MaxPoints 为 0 时默认 1000，正值最多 1000；负值无效。
type Query struct {
	DeviceID   int64
	From, To   time.Time
	Resolution string
	MaxPoints  int
}

// Result 的 Resolution 是实际读取的 raw 或 1m；Points 最多 MaxPoints 个。
type Result struct {
	Resolution string  `json:"resolution"`
	Points     []Point `json:"points"`
}

func validNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func decodeRaw(data string) (map[string]AggregateValue, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &values); err != nil {
		return nil, err
	}
	result := make(map[string]AggregateValue)
	for key, raw := range values {
		spec, ok := historyKey(key)
		if !ok {
			continue
		}
		if string(raw) == "null" {
			return nil, fmt.Errorf("field %s: null value", key)
		}
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("field %s: %w", key, err)
		}
		if !validNumber(value) {
			return nil, fmt.Errorf("field %s: nonfinite value", key)
		}
		if spec.Kind == "bool" && value != 0 && value != 1 {
			return nil, fmt.Errorf("field %s: invalid bool", key)
		}
		result[key] = AggregateValue{Min: value, Max: value, Avg: value, Count: 1}
	}
	return result, nil
}

func decodeAggregate(data string) (map[string]AggregateValue, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &values); err != nil {
		return nil, err
	}
	result := make(map[string]AggregateValue)
	for key, raw := range values {
		if _, ok := historyKey(key); !ok {
			continue
		}
		var value AggregateValue
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if value.Count <= 0 || !validNumber(value.Min) || !validNumber(value.Max) || !validNumber(value.Avg) || value.Min > value.Avg || value.Avg > value.Max {
			return nil, fmt.Errorf("field %s: invalid aggregate", key)
		}
		result[key] = value
	}
	return result, nil
}

func mergeFields(dst, src map[string]AggregateValue) {
	for key, value := range src {
		prev, ok := dst[key]
		if !ok {
			dst[key] = value
			continue
		}
		count := prev.Count + value.Count
		// 先归一化权重，避免 sum 在有限输入上溢出。
		prev.Avg = prev.Avg*(float64(prev.Count)/float64(count)) + value.Avg*(float64(value.Count)/float64(count))
		prev.Min = min(prev.Min, value.Min)
		prev.Max = max(prev.Max, value.Max)
		prev.Count = count
		prev.Avg = min(prev.Max, max(prev.Min, prev.Avg))
		dst[key] = prev
	}
}

// ceilMillis 保持 SQLite 毫秒存储下的严格半开边界，包括亚毫秒查询时间。
func ceilMillis(at time.Time) int64 {
	ms := at.UnixMilli()
	if at.After(time.UnixMilli(ms)) {
		ms++
	}
	return ms
}

// Query 从已提交数据查询，最多持有 1000 个桶，不自动刷新写队列。
// 1m 的边界作用于分钟起点，部分分钟不拆分或伪造精确统计。
func (w *Writer) Query(ctx context.Context, q Query) (Result, error) {
	result := Result{Points: []Point{}}
	if q.DeviceID <= 0 || q.From.IsZero() || q.To.IsZero() || !q.From.Before(q.To) || q.MaxPoints < 0 {
		return result, errors.New("history: invalid query range, device or point limit")
	}
	if q.To.Sub(q.From) == time.Duration(math.MaxInt64) {
		return result, errors.New("history: query range too large")
	}
	if q.MaxPoints == 0 || q.MaxPoints > 1000 {
		q.MaxPoints = 1000
	}
	resolution := q.Resolution
	if resolution == "" || resolution == "auto" {
		resolution = "1m"
		detailRetention, _, _ := w.retention()
		if q.To.Sub(q.From) <= 6*time.Hour && !q.From.Before(time.Now().Add(-detailRetention)) {
			resolution = "raw"
		}
	}
	if resolution != "raw" && resolution != "1m" {
		return result, errors.New("history: resolution must be auto, raw or 1m")
	}
	result.Resolution = resolution
	from, to := ceilMillis(q.From), ceilMillis(q.To)
	if from >= to {
		return result, nil
	}
	table, column := "history_sample", "ts"
	decode := decodeRaw
	if resolution == "1m" {
		table, column = "history_1m", "bucket"
		decode = decodeAggregate
	}
	// 表名/列名仅从上面的固定常量选择，不拼接用户输入。
	rows, err := w.db.Read().QueryContext(ctx, "SELECT "+column+",data FROM "+table+" WHERE device_id=? AND "+column+">=? AND "+column+"<? ORDER BY "+column, q.DeviceID, from, to)
	if err != nil {
		return result, fmt.Errorf("history: query: %w", err)
	}
	defer rows.Close()
	width := (to-from-1)/int64(q.MaxPoints) + 1
	var lastIndex int64 = -1
	for rows.Next() {
		var ts int64
		var data string
		if err := rows.Scan(&ts, &data); err != nil {
			return result, err
		}
		fields, err := decode(data)
		if err != nil {
			return result, fmt.Errorf("history: decode query row: %w", err)
		}
		if len(fields) == 0 {
			continue
		}
		index := (ts - from) / width
		if index != lastIndex {
			result.Points = append(result.Points, Point{At: time.UnixMilli(ts).UTC(), Fields: fields})
			lastIndex = index
		} else {
			point := &result.Points[len(result.Points)-1]
			point.At = time.UnixMilli(from + index*width).UTC()
			mergeFields(point.Fields, fields)
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("history: query rows: %w", err)
	}
	return result, nil
}
