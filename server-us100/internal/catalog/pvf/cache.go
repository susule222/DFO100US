package pvf

import (
	"container/list"
	"sync"
)

// valueCache is a bounded LRU for decompressed archive data. Importers stream
// every group of a 390 MB archive; without a cap the decompressed chunks stay
// resident forever and push the process into gigabytes. Entries are evicted
// least-recently-used once the total byte budget is exceeded, so sequential
// scans (the importer pattern) hold only a bounded window, while random reads
// over the same hot groups still hit the cache.
type valueCache struct {
	mu     sync.Mutex
	budget int
	used   int
	ll     *list.List
	items  map[any]*list.Element
}

type cacheEntry struct {
	key  any
	val  any
	size int
}

func newValueCache(budget int) *valueCache {
	return &valueCache{
		budget: budget,
		ll:     list.New(),
		items:  make(map[any]*list.Element),
	}
}

// entrySize measures the retained value. []byte values (chunks, scripts)
// are charged their length; everything else a fixed small amount.
func entrySize(val any) int {
	if b, ok := val.([]byte); ok {
		return len(b)
	}
	if s, ok := val.(string); ok {
		return len(s)
	}
	return 128
}

func (c *valueCache) Load(key any) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*cacheEntry).val, true
	}
	return nil, false
}

func (c *valueCache) LoadOrStore(key any, val any) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*cacheEntry).val, true
	}
	size := entrySize(val)
	el := c.ll.PushFront(&cacheEntry{key: key, val: val, size: size})
	c.items[key] = el
	c.used += size
	for c.used > c.budget && c.ll.Len() > 1 {
		back := c.ll.Back()
		entry := back.Value.(*cacheEntry)
		c.used -= entry.size
		delete(c.items, entry.key)
		c.ll.Remove(back)
	}
	return val, false
}

func (c *valueCache) Range(f func(key, val any) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*cacheEntry)
		if !f(entry.key, entry.val) {
			return
		}
	}
}

func (c *valueCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
