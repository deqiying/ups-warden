// Package audit 在调用方事务中保存字段级差异，不保存敏感字段值。
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Record 描述一次操作；Sensitive 可以指定额外的字段名或点分路径。
type Record struct {
	UserID                   int64
	IP, Kind, Target, Result string
	Before, After            map[string]any
	Sensitive                []string
}

// Write 只使用传入事务；任何编码或插入失败都交由调用方回滚。
// diff 的键为点分字段路径，普通字段包含 before/after，敏感字段为“已修改”。
func Write(ctx context.Context, tx *sql.Tx, r Record) error {
	if tx == nil {
		return errors.New("audit: 必须提供事务")
	}
	before, err := normalize(r.Before)
	if err != nil {
		return err
	}
	after, err := normalize(r.After)
	if err != nil {
		return err
	}
	extra := make(map[string]bool)
	for _, key := range r.Sensitive {
		extra[canonical(key)] = true
	}
	diff := make(map[string]any)
	changes(diff, "", before, after, extra)
	data, err := json.Marshal(diff)
	if err != nil {
		return fmt.Errorf("audit: 编码差异失败: %w", err)
	}
	var user any
	if r.UserID != 0 {
		user = r.UserID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(at,user_id,ip,kind,target,diff,result) VALUES(?,?,?,?,?,?,?)`, time.Now().UTC().UnixMilli(), user, r.IP, r.Kind, r.Target, string(data), r.Result)
	if err != nil {
		return fmt.Errorf("audit: 写入失败: %w", err)
	}
	return nil
}

func normalize(m map[string]any) (map[string]any, error) {
	// 先验证原始对象，不能让敏感字段掩码隐藏 NaN、循环引用或不可编码值。
	b, err := json.Marshal(m)
	if err != nil {
		return nil, errors.New("audit: 字段不能编码为 JSON")
	}
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, errors.New("audit: 无效 JSON 字段")
	}
	return out, nil
}
func canonical(s string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(s))
}
func sensitive(key, path string, extra map[string]bool) bool {
	k := canonical(key)
	if extra[k] || extra[canonical(path)] {
		return true
	}
	for _, part := range []string{"password", "passwd", "passphrase", "community", "token", "secret", "privatekey", "authorization", "credential", "cookie", "header", "apikey"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return k == "pass" || k == "csrf" || k == "newpass"
}
func clean(v any, path string, extra map[string]bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if sensitive(k, p, extra) {
				out[k] = "已修改"
			} else {
				out[k] = clean(v, p, extra)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = clean(v, path, extra)
		}
		return out
	default:
		return v
	}
}
func changes(out map[string]any, path string, a, b map[string]any, extra map[string]bool) {
	keys := make(map[string]bool)
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		av, aok := a[k]
		bv, bok := b[k]
		if aok == bok && reflect.DeepEqual(av, bv) {
			continue
		}
		p := k
		if path != "" {
			p = path + "." + k
		}
		if sensitive(k, p, extra) {
			out[p] = "已修改"
			continue
		}
		am, ais := av.(map[string]any)
		bm, bis := bv.(map[string]any)
		if (ais || !aok) && (bis || !bok) && (ais || bis) {
			changes(out, p, am, bm, extra)
			continue
		}
		out[p] = map[string]any{"before": clean(av, p, extra), "after": clean(bv, p, extra)}
	}
}
