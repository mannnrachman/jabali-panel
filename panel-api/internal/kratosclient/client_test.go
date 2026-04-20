package kratosclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
)

func TestWhoami_ValidSessionReturnsIdentity(t *testing.T) {
	t.Parallel()

	// Mock Kratos server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sessions/whoami", r.URL.Path)

		// Check that the session cookie was sent.
		cookie, err := r.Cookie("ory_kratos_session")
		require.NoError(t, err)
		assert.Equal(t, "test-session-123", cookie.Value)

		w.Header().Set("Content-Type", "application/json")
		// Real Kratos shape: a Session envelope with `identity` nested. The
		// top-level `id` is the SESSION id and MUST NOT leak into Identity.ID
		// (which is what our users.kratos_identity_id column references).
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "session-abc",
			"active": true,
			"identity": map[string]any{
				"id": "user-456",
				"traits": map[string]any{
					"email":    "user@example.com",
					"username": "testuser",
					"is_admin": true,
				},
			},
		})
	}))
	defer server.Close()

	client := kratosclient.NewClient(server.URL, server.URL)
	identity, err := client.Whoami(context.Background(), "test-session-123")

	require.NoError(t, err)
	assert.Equal(t, "user-456", identity.ID, "must be identity.id, not session.id")
	assert.Equal(t, "user@example.com", identity.GetTraitEmail())
	assert.Equal(t, "testuser", identity.GetTraitUsername())
	assert.True(t, identity.GetTraitIsAdmin())
}

func TestWhoami_InvalidSessionReturns401(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthenticated"})
	}))
	defer server.Close()

	client := kratosclient.NewClient(server.URL, server.URL)
	identity, err := client.Whoami(context.Background(), "invalid-session")

	assert.ErrorIs(t, err, kratosclient.ErrUnauthenticated)
	assert.Nil(t, identity)
}

func TestWhoami_EmptyCookieReturnsError(t *testing.T) {
	t.Parallel()

	client := kratosclient.NewClient("http://localhost:4433", "http://localhost:4434")
	identity, err := client.Whoami(context.Background(), "")

	assert.ErrorIs(t, err, kratosclient.ErrUnauthenticated)
	assert.Nil(t, identity)
}

func TestWhoami_CacheHitSkipsRemoteCall(t *testing.T) {
	t.Parallel()

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "session-cache-test",
			"identity": map[string]any{
				"id":     "user-123",
				"traits": map[string]any{"email": "user@example.com"},
			},
		})
	}))
	defer server.Close()

	client := kratosclient.NewClient(server.URL, server.URL)

	// First call: cache miss, remote call.
	identity1, err := client.Whoami(context.Background(), "session-1")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Second call: cache hit, no remote call.
	identity2, err := client.Whoami(context.Background(), "session-1")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "should not increment on cache hit")

	assert.Equal(t, identity1.ID, identity2.ID)
}

func TestWhoami_DifferentCookiesRequireSeparateCalls(t *testing.T) {
	t.Parallel()

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		cookie, _ := r.Cookie("ory_kratos_session")
		w.Header().Set("Content-Type", "application/json")

		userID := "user-for-" + cookie.Value
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "session-" + cookie.Value,
			"identity": map[string]any{
				"id":     userID,
				"traits": map[string]any{"email": "user@example.com"},
			},
		})
	}))
	defer server.Close()

	client := kratosclient.NewClient(server.URL, server.URL)

	identity1, err := client.Whoami(context.Background(), "session-1")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	identity2, err := client.Whoami(context.Background(), "session-2")
	require.NoError(t, err)
	assert.Equal(t, 2, callCount)

	assert.NotEqual(t, identity1.ID, identity2.ID)
}

func TestIdentity_TraitExtraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		traits map[string]interface{}
		email  string
		user   string
		admin  bool
	}{
		{
			name:   "all_fields_present",
			traits: map[string]interface{}{"email": "user@example.com", "username": "alice", "is_admin": true},
			email:  "user@example.com",
			user:   "alice",
			admin:  true,
		},
		{
			name:   "missing_fields",
			traits: map[string]interface{}{},
			email:  "",
			user:   "",
			admin:  false,
		},
		{
			name:   "is_admin_string_true",
			traits: map[string]interface{}{"is_admin": "true"},
			admin:  true,
		},
		{
			name:   "is_admin_string_false",
			traits: map[string]interface{}{"is_admin": "false"},
			admin:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := &kratosclient.Identity{ID: "user-123", Traits: tt.traits}

			assert.Equal(t, tt.email, identity.GetTraitEmail())
			assert.Equal(t, tt.user, identity.GetTraitUsername())
			assert.Equal(t, tt.admin, identity.GetTraitIsAdmin())
		})
	}
}
