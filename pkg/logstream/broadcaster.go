package logstream

import (
	"sync"
)

// Broadcaster 提供高吞吐、零阻塞的多路日志广播能力。
// 任何写入 Broadcaster 的字节流，都会实时分发给当前所有活跃的订阅者。
type Broadcaster struct {
	mu          sync.RWMutex
	subscribers map[chan []byte]struct{}
	closed      bool
}

// NewBroadcaster 实例化广播器
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[chan []byte]struct{}),
	}
}

// Write 实现 io.Writer 接口。
// 遵循热路径零开销原则，采用非阻塞投放，防止慢网络客户端拖慢执行进程。
func (b *Broadcaster) Write(p []byte) (n int, err error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed || len(b.subscribers) == 0 {
		return len(p), nil
	}

	// 拷贝数据切片，避免缓冲区复用竞争
	data := make([]byte, len(p))
	copy(data, p)

	for ch := range b.subscribers {
		select {
		case ch <- data:
		default:
			// 缓冲区溢出防御：订阅通道已满则非阻塞跳过，坚决杜绝挂起子进程 IO
		}
	}

	return len(p), nil
}

// Subscribe 注册一个日志流订阅通道，返回数据 channel 与取消订阅函数
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 提供 256 个块的环形缓冲队列
	ch := make(chan []byte, 256)
	if b.closed {
		close(ch)
		return ch, func() {}
	}

	b.subscribers[ch] = struct{}{}

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
	}

	return ch, unsubscribe
}

// Close 关闭广播器并清理所有活跃订阅通道
func (b *Broadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true

	for ch := range b.subscribers {
		close(ch)
		delete(b.subscribers, ch)
	}
}
