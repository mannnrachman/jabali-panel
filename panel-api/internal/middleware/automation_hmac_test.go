// HMAC middleware end-to-end tests. Hot-path security code — bug here
// = privilege escalation across every M44 automation route, so the
// matrix below covers every reject branch + a positive control.
package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// fakeAutoTokenRepo is a hand-rolled stub. Avoids sqlmock for tests
// that don't need to assert query shape — the middleware only cares
// about FindByID + BumpLastUsed.
type fakeAutoTokenRepo struct {
	tokens map[string]*models.AutomationToken
}

func (f *fakeAutoTokenRepo) Create(_ context.Context, _ *models.AutomationToken) error {
	return errors.New("not used in tests")
}
func (f *fakeAutoTokenRepo) List(_ context.Context) ([]models.AutomationToken, error) {
	return nil, errors.New("not used in tests")
}
func (f *fakeAutoTokenRepo) FindByID(_ context.Context, id string) (*models.AutomationToken, error) {
	t, ok := f.tokens[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return t, nil
}
func (f *fakeAutoTokenRepo) Revoke(_ context.Context, _ string) error            { return nil }
func (f *fakeAutoTokenRepo) BumpLastUsed(_ context.Context, _, _ string) error    { return nil }

func newTestKey(t *testing.T) *ssokey.Key {
	t.Helper()
	var k ssokey.Key
	for i := range k {
		k[i] = byte(i + 1) // deterministic; not zeroed (zero key is also valid AES-GCM but harder to spot)
	}
	return &k
}

func mintTestToken(t *testing.T, repo *fakeAutoTokenRepo, k *ssokey.Key, scopes models.AutomationScopes) (id, secret string) {
	t.Helper()
	id = "01TESTTOKENABCDEFGHJKMNPQR" // 26-char placeholder, content doesn't matter
	secret = "fa1afe1deadbeef000000000000000000000000000000000000000000000001"
	enc, err := k.Seal([]byte(secret))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	repo.tokens[id] = &models.AutomationToken{
		ID:        id,
		Name:      "test",
		Scopes:    scopes,
		SecretEnc: enc,
		CreatedAt: time.Now().UTC(),
	}
	return id, secret
}

// signRequest computes the canonical HMAC over METHOD || PATH || ts ||
// hex(sha256(BODY)) — same shape the middleware expects.
func signRequest(secret, method, path, ts, body string) string {
	bodyHash := sha256.Sum256([]byte(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(path))
	mac.Write([]byte("\n"))
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write([]byte(hex.EncodeToString(bodyHash[:])))
	return hex.EncodeToString(mac.Sum(nil))
}

func setupHMACRouter(repo *fakeAutoTokenRepo, k *ssokey.Key) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/p", RequireAutomationHMAC(repo, k, nil), func(c *gin.Context) {
		tok := AutomationToken(c)
		c.JSON(http.StatusOK, gin.H{"name": tok.Name})
	})
	return r
}

func doSignedRequest(t *testing.T, r *gin.Engine, kid, secret, ts, path string) int {
	t.Helper()
	sig := signRequest(secret, "GET", path, ts, "")
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", fmt.Sprintf("Jabali-HMAC kid=%s, ts=%s, sig=%s", kid, ts, sig))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRequireAutomationHMAC_HappyPath(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:*"})
	r := setupHMACRouter(repo, k)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	if code := doSignedRequest(t, r, id, secret, ts, "/p"); code != http.StatusOK {
		t.Fatalf("happy path: want 200, got %d", code)
	}
}

func TestRequireAutomationHMAC_RejectsTamperedSig(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, _ := mintTestToken(t, repo, k, models.AutomationScopes{"read:*"})
	r := setupHMACRouter(repo, k)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	// Wrong secret = wrong sig.
	bogus := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	if code := doSignedRequest(t, r, id, bogus, ts, "/p"); code != http.StatusUnauthorized {
		t.Fatalf("tampered sig: want 401, got %d", code)
	}
}

func TestRequireAutomationHMAC_RejectsStaleTimestamp(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:*"})
	r := setupHMACRouter(repo, k)

	stale := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())
	if code := doSignedRequest(t, r, id, secret, stale, "/p"); code != http.StatusUnauthorized {
		t.Fatalf("stale ts: want 401, got %d", code)
	}
}

func TestRequireAutomationHMAC_RejectsFutureTimestamp(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:*"})
	r := setupHMACRouter(repo, k)

	future := fmt.Sprintf("%d", time.Now().Add(10*time.Minute).Unix())
	if code := doSignedRequest(t, r, id, secret, future, "/p"); code != http.StatusUnauthorized {
		t.Fatalf("future ts: want 401, got %d", code)
	}
}

func TestRequireAutomationHMAC_RejectsRevokedToken(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:*"})
	now := time.Now().UTC()
	repo.tokens[id].RevokedAt = &now
	r := setupHMACRouter(repo, k)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	if code := doSignedRequest(t, r, id, secret, ts, "/p"); code != http.StatusUnauthorized {
		t.Fatalf("revoked: want 401, got %d", code)
	}
}

func TestRequireAutomationHMAC_RejectsUnknownKid(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	r := setupHMACRouter(repo, k)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	if code := doSignedRequest(t, r, "01NOTAREALKIDXXXXXXXXXXXX", "what", ts, "/p"); code != http.StatusUnauthorized {
		t.Fatalf("unknown kid: want 401, got %d", code)
	}
}

func TestRequireAutomationHMAC_RejectsMissingHeader(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	r := setupHMACRouter(repo, k)

	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no header: want 401, got %d", w.Code)
	}
}

func TestRequireAutomationHMAC_RejectsMalformedHeader(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	r := setupHMACRouter(repo, k)

	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header.Set("Authorization", "Bearer not-a-jabali-hmac")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong scheme: want 401, got %d", w.Code)
	}
}

func TestRequireAutomationHMAC_PathSensitive(t *testing.T) {
	// Sig over /p must NOT validate against /q. Operator concern: a
	// caller can't sign once + reuse the auth across endpoints.
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:*"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/p", RequireAutomationHMAC(repo, k, nil), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/q", RequireAutomationHMAC(repo, k, nil), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	ts := fmt.Sprintf("%d", time.Now().Unix())
	sigForP := signRequest(secret, "GET", "/p", ts, "")

	req := httptest.NewRequest(http.MethodGet, "/q", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Jabali-HMAC kid=%s, ts=%s, sig=%s", id, ts, sigForP))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("path swap: want 401, got %d", w.Code)
	}
}

func TestRequireScope_RejectsMissingScope(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:status"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/p",
		RequireAutomationHMAC(repo, k, nil),
		RequireScope("read:domains"),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) },
	)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	if code := doSignedRequest(t, r, id, secret, ts, "/p"); code != http.StatusForbidden {
		t.Fatalf("scope rejected: want 403, got %d", code)
	}
}

func TestRequireScope_AllowsExactMatch(t *testing.T) {
	repo := &fakeAutoTokenRepo{tokens: map[string]*models.AutomationToken{}}
	k := newTestKey(t)
	id, secret := mintTestToken(t, repo, k, models.AutomationScopes{"read:domains"})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/p",
		RequireAutomationHMAC(repo, k, nil),
		RequireScope("read:domains"),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) },
	)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	if code := doSignedRequest(t, r, id, secret, ts, "/p"); code != http.StatusOK {
		t.Fatalf("exact scope: want 200, got %d", code)
	}
}

// silence the "unused" lint on `strings` if we drop it later.
var _ = strings.TrimSpace
