package collector

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/normalize"
	"ups-warden/internal/profile"
)

func wait(ctx context.Context, delay time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func pollBudget(source Source) time.Duration {
	// 构造入口已限定周期和重试范围，直接计算即可；总预算最多占周期的 90%。
	return min(source.PollInterval*9/10, source.Timeout*time.Duration(source.Retries+1))
}
func backoff(failures int) time.Duration {
	if failures > 5 {
		return 30 * time.Second
	}
	return time.Second << max(0, failures-1)
}

func acquire(ctx context.Context, gate chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case gate <- struct{}{}:
		if ctx.Err() != nil {
			<-gate
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func pollDriver(ctx context.Context, d driver.Driver) (raw driver.RawData, err error) {
	defer func() {
		if recover() != nil {
			err = ErrPanic
		}
	}()
	raw, err = d.Poll(ctx)
	if ctx.Err() != nil {
		return raw, ctx.Err()
	}
	if err != nil {
		return raw, safeDriverError(err)
	}
	return raw, nil
}

func observation(snapshot model.SourceSnapshot) bool {
	// 身份、额定值和 Profile 静态相数不能证明本轮有有效实时观测。
	for key, field := range snapshot.Fields {
		spec, ok := model.Lookup(key)
		if !ok || field.Quality != model.Good || spec.Group == "ident" || spec.Group == "nominal" || spec.Derived || spec.Kind == model.Text || key == "ups_type" || key == "buzzer_muted" {
			continue
		}
		if spec.Kind == model.Enum && (field.Str == "" || field.Str == "unknown") {
			continue
		}
		return true
	}
	return false
}

func (s *Supervisor) pollLoop(ctx context.Context, w *worker) {
	jitter := s.opts.Jitter(w.source.PollInterval)
	jitter = max(0, min(jitter, w.source.PollInterval))
	if !wait(ctx, jitter) {
		return
	}
	failures := 0
	for {
		start := time.Now()
		pollCtx, cancel := context.WithTimeout(ctx, pollBudget(w.source))
		var raw driver.RawData
		var err error
		if !acquire(pollCtx, w.gate) {
			err = pollCtx.Err()
		} else {
			for attempt := 0; ; attempt++ {
				// 每次尝试也受 request timeout 约束，总截止时间不会因重试重置。
				requestCtx, requestCancel := context.WithTimeout(pollCtx, w.source.Timeout)
				if w.driver == nil {
					var plan driver.ReadPlan
					plan, err = validate(w.source)
					if err == nil {
						w.driver, err = makeDriver(s.opts.Factory, w.source, plan)
					}
				}
				if w.driver != nil {
					raw, err = pollDriver(requestCtx, w.driver)
				}
				requestCancel()
				if err == nil || errors.Is(err, ErrPanic) || pollCtx.Err() != nil || attempt == w.source.Retries {
					break
				}
			}
			<-w.gate
		}
		if pollCtx.Err() != nil {
			err = pollCtx.Err()
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		raw = cloneRaw(raw)
		raw.At = time.Now().UTC()
		var snapshot model.SourceSnapshot
		var trace []normalize.Trace
		if raw.Protocol == w.source.Protocol {
			var normErr error
			snapshot, trace, normErr = normalize.Normalize(w.source.ID, raw, w.source.Profile)
			if err == nil && normErr != nil {
				err = ErrConfig
			}
		} else if err == nil {
			err = ErrNoObservation
		}
		if err == nil && !observation(snapshot) {
			err = ErrNoObservation
		}
		// 去抖、乱序判断和失败计时必须保留单调读数，UTC 仅用于持久化/输出。
		at := time.Now()
		latency := time.Since(start)
		if err != nil {
			failures++
		} else {
			failures = 0
		}
		s.mu.Lock()
		st := s.statuses[w.source.ID]
		st.Latency = latency
		st.Failures = failures
		st.ErrorCode = errorCode(err)
		if err == nil {
			st.LastOKAt = at
		}
		s.statuses[w.source.ID] = st
		s.cache[w.source.ID] = diagnostics{raw: raw, trace: cloneTrace(trace)}
		s.mu.Unlock()
		result := model.SourceResult{DeviceID: w.source.DeviceID, SourceID: w.source.ID, At: at, Failed: err != nil, Latency: latency}
		if err == nil {
			result.Snapshot = snapshot
		}
		if ctx.Err() != nil {
			return
		}
		if sinkErr := s.sink(ctx, result); sinkErr != nil {
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			st := s.statuses[w.source.ID]
			st.ErrorCode = "sink_failed"
			s.statuses[w.source.ID] = st
			s.mu.Unlock()
			s.opts.Logger.Error("采集结果投递失败", "source_id", w.source.ID, "code", "sink_failed")
		}
		if err != nil {
			s.opts.Logger.Warn("采集失败", "source_id", w.source.ID, "code", errorCode(err))
		}
		// panic 后驱动内部状态不可信；关闭后由同一 worker 退避重建，
		// 继续产生失败事实，不能永久停采而让通信状态停留在 ok。
		if errors.Is(err, ErrPanic) && w.driver != nil {
			s.recordClose(w.driver)
			w.driver = nil
		}
		delay := w.source.PollInterval
		if failures != 0 {
			delay = backoff(failures)
		}
		if !wait(ctx, delay) {
			return
		}
	}
}

// TestResult 包含临时 Probe 的归一化结果、诊断和 Profile 推荐，不改变 worker 缓存。
type TestResult struct {
	Latency         time.Duration
	DeviceInfo      driver.DeviceInfo
	Snapshot        model.SourceSnapshot
	Raw             driver.RawData
	Trace           []normalize.Trace
	Recommendations []profile.Recommendation
}

// Test 临时构造驱动；总限额默认 5s，Timeout 非零则作为总限额（最大 1min）。
// 与 worker 共用 canonical IP 信号量；等待超时返回 ErrBusy，关闭时取消并等待。
func (s *Supervisor) Test(ctx context.Context, source Source) (result TestResult, err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return result, ErrClosed
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	if source.Timeout == 0 {
		source.Timeout = 5 * time.Second
	}
	if source.PollInterval == 0 {
		source.PollInterval = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout)
	defer cancel()
	stopLife := context.AfterFunc(s.life, cancel)
	defer stopLife()
	start := time.Now()
	defer func() {
		result.Latency = time.Since(start)
		if recover() != nil {
			err = ErrPanic
		}
	}()
	w, err := s.prepare(ctx, source)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, closeDriver(w.driver)) }()
	if !acquire(ctx, w.gate) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, ErrBusy
		}
		return result, ctx.Err()
	}
	defer func() { <-w.gate }()
	result.DeviceInfo, result.Raw, err = probeDriver(ctx, w.driver)
	result.Raw = cloneRaw(result.Raw)
	result.Raw.At = time.Now().UTC()
	if err != nil {
		return result, err
	}
	result.Snapshot, result.Trace, err = normalize.Normalize(source.ID, result.Raw, source.Profile)
	if err != nil {
		return result, ErrConfig
	}
	result.Trace = cloneTrace(result.Trace)
	if !observation(result.Snapshot) {
		return result, ErrNoObservation
	}
	candidates, err := s.recommendationProfiles(ctx)
	if err != nil {
		return result, err
	}
	result.Recommendations = profile.Recommend(source.Protocol, result.DeviceInfo, candidates)
	return result, nil
}

func probeDriver(ctx context.Context, d driver.Driver) (info driver.DeviceInfo, raw driver.RawData, err error) {
	defer func() {
		if recover() != nil {
			err = ErrPanic
		}
	}()
	info, raw, err = d.Probe(ctx)
	if ctx.Err() != nil {
		return info, raw, ctx.Err()
	}
	if err != nil {
		return info, raw, safeDriverError(err)
	}
	return info, raw, nil
}

func (s *Supervisor) recommendationProfiles(ctx context.Context) ([]*profile.Profile, error) {
	candidates, err := profile.Builtins()
	if err != nil {
		return nil, ErrConfig
	}
	if s.db == nil {
		return candidates, nil
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT key,protocol,version,content FROM profiles ORDER BY key`)
	if err != nil {
		return nil, ErrStore
	}
	defer rows.Close()
	for rows.Next() {
		var key, protocol, content string
		var version int
		if rows.Scan(&key, &protocol, &version, &content) != nil {
			return nil, ErrStore
		}
		if profile.IsBuiltinKey(key) {
			continue
		}
		p, err := profile.Parse([]byte(content))
		if err != nil || p.Key != key || p.Protocol != protocol || p.Version != version {
			return nil, ErrConfig
		}
		candidates = append(candidates, p)
	}
	if rows.Err() != nil {
		return nil, ErrStore
	}
	return candidates, nil
}

func cloneRaw(raw driver.RawData) driver.RawData {
	raw.SNMP = maps.Clone(raw.SNMP)
	for key, value := range raw.SNMP {
		value.Bytes = slices.Clone(value.Bytes)
		raw.SNMP[key] = value
	}
	raw.Modbus = maps.Clone(raw.Modbus)
	raw.Errors = slices.Clone(raw.Errors)
	if raw.AYI9 != nil {
		copy := *raw.AYI9
		copy.QAD = slices.Clone(copy.QAD)
		copy.RTD = slices.Clone(copy.RTD)
		raw.AYI9 = &copy
	}
	return raw
}
func cloneValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return slices.Clone(v)
	case []uint16:
		return slices.Clone(v)
	case []string:
		return slices.Clone(v)
	default:
		return value // Normalize 的其他值只能是不可变标量。
	}
}
func cloneTrace(trace []normalize.Trace) []normalize.Trace {
	trace = slices.Clone(trace)
	for i := range trace {
		trace[i].Raw = cloneValue(trace[i].Raw)
		trace[i].Decoded = cloneValue(trace[i].Decoded)
	}
	return trace
}
