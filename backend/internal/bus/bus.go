// Package bus 提供设备更新的有损扇出；慢订阅者必须重连并重新读取全量快照。
package bus

import (
	"sync"
	"ups-warden/internal/model"
)

// Bus 的零值可用；每订阅者固定缓冲 256 条，溢出直接关闭，不影响发布者。
type Bus struct {
	mu      sync.Mutex
	next    uint64
	subs    map[uint64]chan model.DeviceUpdate
	closed  bool
	dropped uint64
}

// Subscribe 返回接收通道与幂等取消函数；总线关闭后返回已关闭通道。
func (b *Bus) Subscribe() (<-chan model.DeviceUpdate, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan model.DeviceUpdate, 256)
	if b.closed {
		close(ch)
		return ch, func() {}
	}
	if b.subs == nil {
		b.subs = map[uint64]chan model.DeviceUpdate{}
	}
	b.next++
	id := b.next
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if current, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(current)
		}
	}
}

// Publish 为每个订阅者复制数据；不阻塞等待接收。
func (b *Bus) Publish(u model.DeviceUpdate) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for id, ch := range b.subs {
		select {
		case ch <- model.CloneUpdate(u):
		default:
			close(ch)
			delete(b.subs, id)
			b.dropped++
		}
	}
}

// Dropped 返回因溢出断开的订阅数。
func (b *Bus) Dropped() uint64 { b.mu.Lock(); defer b.mu.Unlock(); return b.dropped }

// Close 幂等关闭所有订阅。
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		close(ch)
		delete(b.subs, id)
	}
}
