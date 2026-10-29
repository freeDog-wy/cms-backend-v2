package pkg

import "sync"

type KeyConcurrencyLimiter interface {
	TryAcquire(key string) (release func(), acquired bool)
}

type MemoryConcurrencyLimiter struct {
	mu sync.Mutex

	active map[string]int

	limitPerKey int
	maxKeys     int
}

func NewMemoryConcurrencyLimiter(limitPerKey int, maxKeys int) *MemoryConcurrencyLimiter {
	if limitPerKey <= 0 || maxKeys <= 0 {
		panic("invalid concurrency limiter configuration")
	}

	return &MemoryConcurrencyLimiter{
		active:      make(map[string]int),
		limitPerKey: limitPerKey,
		maxKeys:     maxKeys,
	}
}

func (l *MemoryConcurrencyLimiter) TryAcquire(key string) (func(), bool) {
	l.mu.Lock()

	count := l.active[key]

	if count >= l.limitPerKey {
		l.mu.Unlock()
		return nil, false
	}

	// 仅限制新增 Key，已有 Key 仍按各自并发额度处理。
	if count == 0 && len(l.active) >= l.maxKeys {
		l.mu.Unlock()
		return nil, false
	}

	l.active[key] = count + 1
	l.mu.Unlock()

	var once sync.Once

	release := func() {
		// 防止重复释放，导致计数错误或误删后续请求的记录。
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			count := l.active[key]
			if count <= 1 {
				delete(l.active, key)
				return
			}

			l.active[key] = count - 1
		})
	}

	return release, true
}
