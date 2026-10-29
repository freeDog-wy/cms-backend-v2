package pkg

import (
	"sync"
	"time"
)

type KeyCooldown interface {
	Check(key string) time.Duration
	Hit(key string) time.Duration
	Reset(key string)
}

type cooldownState struct {
	hits        int
	windowEnd   time.Time
	lockedUntil time.Time
}

func (s cooldownState) expired(now time.Time) bool {
	if !s.lockedUntil.IsZero() {
		return !now.Before(s.lockedUntil)
	}

	return !now.Before(s.windowEnd)
}

type MemoryCooldown struct {
	mu     sync.Mutex
	states map[string]cooldownState

	threshold int
	window    time.Duration
	cooldown  time.Duration

	nextCleanup time.Time
}

func NewMemoryCooldown(threshold int, window time.Duration, cooldown time.Duration) *MemoryCooldown {
	if threshold <= 0 || window <= 0 || cooldown <= 0 {
		panic("invalid cooldown configuration")
	}

	return &MemoryCooldown{
		states:      make(map[string]cooldownState),
		threshold:   threshold,
		window:      window,
		cooldown:    cooldown,
		nextCleanup: time.Now().Add(time.Minute),
	}
}

// 调用方必须持有 mu。
func (c *MemoryCooldown) getLocked(key string, now time.Time) (cooldownState, bool) {
	// 按需清理，不启动额外 goroutine。
	if !now.Before(c.nextCleanup) {
		for key, state := range c.states {
			if state.expired(now) {
				delete(c.states, key)
			}
		}
		c.nextCleanup = now.Add(time.Minute)
	}

	state, found := c.states[key]
	if found && state.expired(now) {
		delete(c.states, key)
		return cooldownState{}, false
	}

	return state, found
}

func (c *MemoryCooldown) Check(key string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	state, found := c.getLocked(key, now)

	if !found || state.lockedUntil.IsZero() {
		return 0
	}

	return state.lockedUntil.Sub(now)
}

func (c *MemoryCooldown) Hit(key string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	state, found := c.getLocked(key, now)

	// 冷却期间不续期。
	if found && !state.lockedUntil.IsZero() {
		return state.lockedUntil.Sub(now)
	}

	// 从第一次事件开始计算窗口。
	if !found {
		state = cooldownState{
			windowEnd: now.Add(c.window),
		}
	}

	state.hits++

	if state.hits >= c.threshold {
		state.lockedUntil = now.Add(c.cooldown)
	}

	c.states[key] = state

	if !state.lockedUntil.IsZero() {
		return state.lockedUntil.Sub(now)
	}

	return 0
}

func (c *MemoryCooldown) Reset(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.states, key)
}
