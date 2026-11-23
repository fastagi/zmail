package imapgateway

import (
	"container/list"
	"sync"
)

// sourceCache keeps recently downloaded message sources by message id, bounded in bytes. A source
// never changes once stored, so an entry is never stale; access is checked before every read.
type sourceCache struct {
	mu       sync.Mutex
	maxBytes int64
	size     int64
	order    *list.List
	entries  map[uint]*list.Element
}

type sourceEntry struct {
	id  uint
	raw []byte
}

func newSourceCache(maxBytes int64) *sourceCache {
	return &sourceCache{maxBytes: maxBytes, order: list.New(), entries: map[uint]*list.Element{}}
}

// Get returns the cached source and marks it recently used.
func (c *sourceCache) Get(id uint) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[id]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return sourceOf(element).raw, true
}

// Add caches a source, evicting the least recently used ones past the bound. A source larger
// than the whole cache is not kept.
func (c *sourceCache) Add(id uint, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[id]; ok {
		c.remove(element)
	}
	if int64(len(raw)) > c.maxBytes {
		return
	}
	c.entries[id] = c.order.PushFront(&sourceEntry{id: id, raw: raw})
	c.size += int64(len(raw))
	for c.size > c.maxBytes {
		c.remove(c.order.Back())
	}
}

func (c *sourceCache) remove(element *list.Element) {
	entry := sourceOf(element)
	c.order.Remove(element)
	delete(c.entries, entry.id)
	c.size -= int64(len(entry.raw))
}

// Size is the cached bytes.
func (c *sourceCache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// sourceOf is the entry an element of a sourceCache holds.
func sourceOf(element *list.Element) *sourceEntry {
	entry, ok := element.Value.(*sourceEntry)
	if !ok {
		panic("imapgateway: a list element of another type")
	}
	return entry
}
