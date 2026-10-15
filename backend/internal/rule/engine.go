package rule

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"ups-warden/internal/model"
)

// instance 的 fired 是已消费的真边沿，activated 区分实际命中与冷却跳过。
// 元数据存入既有 since JSON，不引入另一套状态表。
type instance struct {
	Fired         bool                 `json:"-"`
	FiredAt       *time.Time           `json:"-"`
	Timers        map[string]time.Time `json:"timers,omitempty"`
	Activated     bool                 `json:"activated,omitempty"`
	QualityReason string               `json:"quality_reason,omitempty"`
	ActiveEvents  []string             `json:"active_events,omitempty"`
	LastEventID   int64                `json:"last_event_id,omitempty"`
	LastEventAt   time.Time            `json:"last_event_at,omitempty"`
}

type decision struct {
	Kind     string
	Reason   string
	Values   map[string]model.Field
	Actions  []ActionRef
	Event    model.Event
	Recovery bool
}

func evaluate(r Record, st *instance, u model.DeviceUpdate) []decision {
	if r.Trigger.Type == "event" {
		return evaluateEvents(r, st, u)
	}
	at := u.State.At
	if st.Timers == nil {
		st.Timers = map[string]time.Time{}
	}
	values := map[string]model.Field{}
	var reasons []string
	matched, definitelyFalse := false, true
	for gi, group := range r.Trigger.AnyOf {
		groupMatch, groupFalse := true, false
		for ci, c := range group.AllOf {
			key := fmt.Sprintf("%d/%d", gi, ci)
			field := model.Resolve(u.State.Fields, c.Field)
			values[c.Field] = field
			if field.Quality != model.Good {
				reasons = append(reasons, c.Field+":"+string(field.Quality)+":"+field.Reason)
				groupMatch = false
				delete(st.Timers, key)
				continue
			}
			if !compare(c, field) {
				groupMatch, groupFalse = false, true
				delete(st.Timers, key)
				continue
			}
			if c.ForS > 0 {
				since, ok := st.Timers[key]
				if !ok {
					since = at
					st.Timers[key] = since
				}
				if at.Sub(since) < time.Duration(c.ForS)*time.Second {
					groupMatch = false
				}
			}
		}
		matched = matched || groupMatch
		definitelyFalse = definitelyFalse && groupFalse
	}
	slices.Sort(reasons)
	reason := strings.Join(slices.Compact(reasons), ";")
	var out []decision
	if reason != st.QualityReason {
		kind := "quality_blocked"
		if reason == "" {
			kind = "quality_restored"
		}
		out = append(out, decision{Kind: kind, Reason: reason, Values: values})
		st.QualityReason = reason
	}
	if matched && !st.Fired {
		// 冷却跳过也消费本次边沿，冷却结束后不能补发。
		st.Fired = true
		if cooling(r, st, at) {
			out = append(out, decision{Kind: "skipped_cooldown", Reason: "cooldown", Values: values})
		} else {
			st.FiredAt = &at
			st.Activated = true
			out = append(out, decision{Kind: "fired", Reason: "condition_matched", Values: values, Actions: r.Actions})
		}
	} else if !matched && definitelyFalse && st.Fired {
		// 不把失联、重启缺测或 for_s 重新计时当作恢复证据。
		st.Fired = false
		if st.Activated {
			out = append(out, decision{Kind: "recovered", Reason: "condition_cleared", Values: values, Actions: r.RecoveryActions, Recovery: true})
		}
		st.Activated = false
	}
	return out
}

func compare(c Condition, f model.Field) bool {
	spec, _ := model.Lookup(c.Field) // Record 已在入口校验。
	var equal bool
	switch spec.Kind {
	case model.Bool:
		var expected bool
		_ = json.Unmarshal(c.Value, &expected)
		equal = f.Bool == expected
	case model.Enum, model.Text:
		var expected string
		_ = json.Unmarshal(c.Value, &expected)
		equal = f.Str == expected
	default:
		var expected float64
		_ = json.Unmarshal(c.Value, &expected)
		switch c.Op {
		case "gt":
			return f.Num > expected
		case "gte":
			return f.Num >= expected
		case "lt":
			return f.Num < expected
		case "lte":
			return f.Num <= expected
		}
		equal = f.Num == expected
	}
	if c.Op == "ne" {
		return !equal
	}
	return equal
}
func cooling(r Record, st *instance, at time.Time) bool {
	return st.FiredAt != nil && at.Sub(*st.FiredAt) < time.Duration(r.CooldownS)*time.Second
}
func evaluateEvents(r Record, st *instance, u model.DeviceUpdate) []decision {
	var out []decision
	lastID, lastAt := st.LastEventID, st.LastEventAt
	events := append(slices.Clone(u.Events), u.ClearedEvents...)
	for _, event := range events {
		// 历史清理后 SQLite 可复用 rowid；后来的事件不能被旧 ID 水位压住。
		if event.ID > 0 && event.ID <= st.LastEventID && !event.At.After(st.LastEventAt) {
			continue
		}
		if event.ID > 0 {
			lastID, lastAt = event.ID, event.At
		}
		if event.Clears != "" && slices.Contains(st.ActiveEvents, event.Clears) {
			st.ActiveEvents = slices.DeleteFunc(st.ActiveEvents, func(kind string) bool { return kind == event.Clears })
			out = append(out, decision{Kind: "recovered", Reason: event.Clears + "_cleared", Values: model.CloneFields(event.Summary), Actions: r.RecoveryActions, Event: event, Recovery: true})
		}
		if !slices.Contains(r.Trigger.Events, event.Type) {
			continue
		}
		if cooling(r, st, event.At) {
			out = append(out, decision{Kind: "skipped_cooldown", Reason: "cooldown", Values: model.CloneFields(event.Summary), Event: event})
			continue
		}
		at := event.At
		st.FiredAt = &at
		if event.Active && !slices.Contains(st.ActiveEvents, event.Type) {
			st.ActiveEvents = append(st.ActiveEvents, event.Type)
		}
		out = append(out, decision{Kind: "fired", Reason: event.Type, Values: model.CloneFields(event.Summary), Actions: r.Actions, Event: event})
	}
	st.LastEventID, st.LastEventAt = lastID, lastAt
	st.Fired = len(st.ActiveEvents) > 0
	return out
}
