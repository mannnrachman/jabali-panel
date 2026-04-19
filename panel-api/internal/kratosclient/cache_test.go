package kratosclient_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
)

func TestCache_SetGet(t *testing.T) {
	t.Parallel()

	cache := kratosclient.NewCache(10, 10*time.Second)
	identity := &kratosclient.Identity{
		ID:     "user-123",
		Traits: map[string]interface{}{"email": "user@example.com"},
	}

	cache.Set("session-cookie-1", identity)
	result, ok := cache.Get("session-cookie-1")

	assert.True(t, ok)
	assert.Equal(t, "user-123", result.ID)
}

func TestCache_GetMissReturnsNil(t *testing.T) {
	t.Parallel()

	cache := kratosclient.NewCache(10, 10*time.Second)

	result, ok := cache.Get("nonexistent")

	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCache_ExpirationEvictsEntries(t *testing.T) {
	t.Parallel()

	cache := kratosclient.NewCache(10, 100*time.Millisecond)
	identity := &kratosclient.Identity{ID: "user-123"}

	cache.Set("session", identity)
	result, ok := cache.Get("session")
	assert.True(t, ok)

	time.Sleep(150 * time.Millisecond)
	result, ok = cache.Get("session")
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCache_MaxSizeEvictsOldest(t *testing.T) {
	t.Parallel()

	cache := kratosclient.NewCache(2, 10*time.Second)
	id1 := &kratosclient.Identity{ID: "user-1"}
	id2 := &kratosclient.Identity{ID: "user-2"}
	id3 := &kratosclient.Identity{ID: "user-3"}

	cache.Set("cookie-1", id1)
	cache.Set("cookie-2", id2)
	cache.Set("cookie-3", id3) // Should evict cookie-1

	_, ok1 := cache.Get("cookie-1")
	_, ok2 := cache.Get("cookie-2")
	_, ok3 := cache.Get("cookie-3")

	assert.False(t, ok1, "oldest entry should be evicted")
	assert.True(t, ok2)
	assert.True(t, ok3)
}

func TestCache_UpdateRefreshesExpiryTime(t *testing.T) {
	t.Parallel()

	cache := kratosclient.NewCache(10, 100*time.Millisecond)
	identity := &kratosclient.Identity{ID: "user-123"}

	cache.Set("session", identity)
	time.Sleep(50 * time.Millisecond)
	cache.Set("session", identity) // Re-set, should reset expiry

	time.Sleep(60 * time.Millisecond) // Total 110ms from first set, but only 60ms from second

	result, ok := cache.Get("session")
	assert.True(t, ok, "entry should still be valid")
	assert.NotNil(t, result)
}

func TestCache_Clear(t *testing.T) {
	t.Parallel()

	cache := kratosclient.NewCache(10, 10*time.Second)
	cache.Set("session-1", &kratosclient.Identity{ID: "user-1"})
	cache.Set("session-2", &kratosclient.Identity{ID: "user-2"})

	cache.Clear()

	_, ok1 := cache.Get("session-1")
	_, ok2 := cache.Get("session-2")

	assert.False(t, ok1)
	assert.False(t, ok2)
}
