package collector

import "context"

// StopDevice 暂停设备全部 worker 并清除诊断；不改数据库，供配置事务前排空旧事实。
// 失败或取消时不启动新 worker；调用方须通过 Reload 恢复已提交配置。
func (s *Supervisor) StopDevice(ctx context.Context, deviceID int64) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	targets := map[int64]*worker{}
	for id, w := range s.workers {
		if w.source.DeviceID == deviceID {
			targets[id] = w
			w.cancel()
		}
	}
	s.mu.Unlock()
	for id, w := range targets {
		select {
		case <-w.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.mu.Lock()
		delete(s.workers, id)
		delete(s.statuses, id)
		delete(s.cache, id)
		s.mu.Unlock()
	}
	return nil
}
