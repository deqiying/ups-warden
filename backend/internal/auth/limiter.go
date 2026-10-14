package auth

import (
	"crypto/sha256"
	"sync"
	"time"
)

const maxRateKeys = 4096

type rateEntry struct {
	failures []time.Time
	pending  int
	locked   time.Time
}
type limiter struct {
	mu      sync.Mutex
	entries map[[32]byte]*rateEntry
}

func rateKeys(user, ip string) [2][32]byte {
	return [2][32]byte{sha256.Sum256([]byte("user:" + user)), sha256.Sum256([]byte("ip:" + ip))}
}

// reserve 同时占用用户名/IP 配额，防止并发失败请求越过五次上限。
func (l *limiter) reserve(user, ip string, now time.Time) ([2][32]byte, error) {
	keys := rateKeys(user, ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = make(map[[32]byte]*rateEntry)
	}
	for k, e := range l.entries {
		i := 0
		for i < len(e.failures) && !now.Before(e.failures[i].Add(15*time.Minute)) {
			i++
		}
		e.failures = e.failures[i:]
		if e.pending == 0 && !now.Before(e.locked) && len(e.failures) == 0 {
			delete(l.entries, k)
		}
	}
	missing := 0
	for _, k := range keys {
		e := l.entries[k]
		if e == nil {
			missing++
			continue
		}
		if now.Before(e.locked) || len(e.failures)+e.pending >= 5 {
			return keys, ErrRateLimited
		}
	}
	// 不驱逐未过期锁定项，否则攻击者可用新名称绕过锁定。
	if len(l.entries)+missing > maxRateKeys {
		return keys, ErrRateLimited
	}
	for _, k := range keys {
		e := l.entries[k]
		if e == nil {
			e = &rateEntry{}
			l.entries[k] = e
		}
		e.pending++
	}
	return keys, nil
}
func (l *limiter) finish(keys [2][32]byte, now time.Time, failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		e := l.entries[k]
		if e == nil {
			continue
		}
		e.pending--
		// 成功请求不清除同一 IP 的既有失败，否则已知账号可绕过来源限速。
		if failed {
			e.failures = append(e.failures, now)
			if len(e.failures) >= 5 {
				e.locked = now.Add(15 * time.Minute)
			}
		}
		if e.pending == 0 && len(e.failures) == 0 && !now.Before(e.locked) {
			delete(l.entries, k)
		}
	}
}
