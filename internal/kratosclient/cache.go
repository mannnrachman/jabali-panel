package kratosclient

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// Cache is a thread-safe LRU cache for session validation results.
// Keys are session cookie values (hashed SHA-256); values are *Identity pointers.
// Entries expire after TTL even if they remain in the cache.
type Cache struct {
	mu       sync.RWMutex
	maxSize  int
	ttl      time.Duration
	entries  map[string]*cacheEntry
	lru      *list.List // *cacheEntry in order of access
}

type cacheEntry struct {
	key       string
	identity  *Identity
	expiresAt time.Time
	elem      *list.Element // ptr back to lru
}

// NewCache returns a new LRU cache with the given max size and entry TTL.
// Size is in entries (not bytes); TTL is the expiration duration.
func NewCache(maxSize int, ttl time.Duration) *Cache {
	return &Cache{
		maxSize: maxSize,
		ttl:     ttl,
		entries: make(map[string]*cacheEntry),
		lru:     list.New(),
	}
}

// Get retrieves an identity from the cache if present and not expired.
// Returns (nil, false) if not found or expired.
func (c *Cache) Get(cookieValue string) (*Identity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := hashCookie(cookieValue)
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		// Entry expired but not yet evicted; don't return it.
		return nil, false
	}

	return entry.identity, true
}

// Set stores an identity in the cache, evicting the oldest entry if necessary.
// The cookie value is hashed before storage; only the hash is kept.
func (c *Cache) Set(cookieValue string, identity *Identity) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := hashCookie(cookieValue)

	// If this key already exists, remove the old entry first.
	if old, ok := c.entries[key]; ok {
		c.lru.Remove(old.elem)
		delete(c.entries, key)
	}

	// Evict oldest entry if at capacity.
	if len(c.entries) >= c.maxSize {
		oldest := c.lru.Front()
		if oldest != nil {
			oldEntry := oldest.Value.(*cacheEntry)
			c.lru.Remove(oldest)
			delete(c.entries, oldEntry.key)
		}
	}

	// Insert new entry.
	entry := &cacheEntry{
		key:       key,
		identity:  identity,
		expiresAt: time.Now().Add(c.ttl),
	}
	entry.elem = c.lru.PushBack(entry)
	c.entries[key] = entry
}

// Clear empties the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	c.lru = list.New()
}

// hashCookie returns the SHA-256 hash of a cookie value.
// We never store the cookie itself, only its hash.
func hashCookie(cookieValue string) string {
	h := sha256.Sum256([]byte(cookieValue))
	return fmt.Sprintf("%x", h)
}
