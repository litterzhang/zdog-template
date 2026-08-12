package common

import "sync"

// Cachable 是惰性求值一次的缓存。复用自旧原型 common/cache.go，
// 补上并发安全（旧版本在多 goroutine 下会重复求值并产生数据竞争）。
type Cachable[V any] interface {
	Get() (V, error)
	Cached() bool
	Load(loader func() (V, error)) Cachable[V]
}

// CachedObject 是 Cachable 的默认实现。零值可用。
type CachedObject[V any] struct {
	mu     sync.RWMutex
	loaded bool
	v      V
	err    error
}

// NewCached 返回一个空的缓存对象。
func NewCached[V any]() *CachedObject[V] {
	return &CachedObject[V]{}
}

// Cached 报告是否已经求值过。
func (c *CachedObject[V]) Cached() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}

// Get 返回已缓存的值。未求值时返回零值。
func (c *CachedObject[V]) Get() (V, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.v, c.err
}

// Load 在首次调用时执行 loader，之后直接返回缓存。
func (c *CachedObject[V]) Load(loader func() (V, error)) Cachable[V] {
	c.mu.RLock()
	if c.loaded {
		c.mu.RUnlock()
		return c
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded { // 双检
		return c
	}
	c.v, c.err = loader()
	c.loaded = true
	return c
}
