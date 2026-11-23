package imapgateway

import (
	"bytes"
	"testing"
)

func TestSourceCacheBounded(t *testing.T) {
	cache := newSourceCache(10)
	cache.Add(1, []byte("aaaa"))
	cache.Add(2, []byte("bbbb"))
	if got, ok := cache.Get(1); !ok || !bytes.Equal(got, []byte("aaaa")) {
		t.Fatalf("Get(1) = %q, %v", got, ok)
	}
	cache.Add(3, []byte("cccc")) // over 10 bytes: evicts 2, the least recently used
	if _, ok := cache.Get(2); ok {
		t.Fatal("the least recently used entry stayed")
	}
	if _, ok := cache.Get(1); !ok {
		t.Fatal("a recently used entry was evicted")
	}
	cache.Add(4, bytes.Repeat([]byte("x"), 11))
	if _, ok := cache.Get(4); ok {
		t.Fatal("an entry larger than the cache was kept")
	}
	if cache.Size() > 10 {
		t.Fatalf("size = %d", cache.Size())
	}
	cache.Add(1, []byte("dd"))
	if got, _ := cache.Get(1); !bytes.Equal(got, []byte("dd")) || cache.Size() != 6 {
		t.Fatalf("replacing = %q, size %d", got, cache.Size())
	}
}
