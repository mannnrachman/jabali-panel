package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
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

// --- fake repos ---

type fakeMailboxRepoSSO struct{ repository.MailboxRepository; mb *models.Mailbox }

func (f *fakeMailboxRepoSSO) FindByID(_ context.Context, id string) (*models.Mailbox, error) {
	if f.mb != nil && f.mb.ID == id {
		return f.mb, nil
	}
	return nil, repository.ErrNotFound
}

type fakeDomainRepoSSO struct{ repository.DomainRepository; dom *models.Domain }

func (f *fakeDomainRepoSSO) FindByID(_ context.Context, id string) (*models.Domain, error) {
	if f.dom != nil && f.dom.ID == id {
		return f.dom, nil
	}
	return nil, repository.ErrNotFound
}

type fakeSSOTokenRepo struct {
	repository.MailboxSSOTokenRepository
	tok *models.MailboxSSOToken
	// consumed set after first consume to simulate FOR UPDATE + DELETE.
	consumed bool
}

func (f *fakeSSOTokenRepo) ConsumeByHash(_ context.Context, hash string) (*models.MailboxSSOToken, error) {
	if f.consumed || f.tok == nil || f.tok.TokenHash != hash {
		return nil, repository.ErrNotFound
	}
	f.consumed = true
	return f.tok, nil
}

// --- helpers ---

// newBulwarkFake returns an httptest server that mimics Bulwark's
// POST /api/auth/session: reads the JSON body, and always returns 200
// + two Set-Cookie headers (mirroring the real behaviour with HttpOnly
// + Secure + SameSite=lax attributes we observed against v1.4.14).
func newBulwarkFake(t *testing.T, wantServerURL, wantUsername string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/session" || r.Method != http.MethodPost {
			t.Errorf("unexpected fake-bulwark hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad route", http.StatusBadRequest)
			return
		}
		var body struct{ ServerURL, Username, Password string }
		// Bulwark's handler uses snake-ish JSON; our client sends
		// {serverUrl, username, password}. Decode flexibly.
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if s, ok := m["serverUrl"].(string); ok {
			body.ServerURL = s
		}
		if s, ok := m["username"].(string); ok {
			body.Username = s
		}
		if s, ok := m["password"].(string); ok {
			body.Password = s
		}
		if body.ServerURL != wantServerURL {
			t.Errorf("bulwark received serverUrl=%q want=%q", body.ServerURL, wantServerURL)
		}
		if body.Username != wantUsername {
			t.Errorf("bulwark received username=%q want=%q", body.Username, wantUsername)
		}
		if body.Password == "" {
			t.Errorf("bulwark received empty password")
		}
		w.Header().Add("Set-Cookie", "jmap_session=SESSIONCOOKIEVALUE; Path=/; HttpOnly; Secure; SameSite=lax; Max-Age=2592000")
		w.Header().Add("Set-Cookie", "jmap_stalwart_ctx=CTXVALUE; Path=/; HttpOnly; Secure; SameSite=lax")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
}

// mintToken produces (plaintextTokenURLSafe, sha256HexHash). Matches
// the minting in mailboxHandler.mintSSO (/domains/:id/mailboxes/:id/sso).
func mintToken(t *testing.T) (string, string) {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	plain := base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256(raw)
	return plain, hex.EncodeToString(h[:])
}

// sealPassword encrypts a plaintext with a deterministic-ish key for
// the test — any 32-byte value is a valid ssokey.Key.
func sealPassword(t *testing.T, key ssokey.Key, plaintext string) []byte {
	t.Helper()
	env, err := key.Seal([]byte(plaintext))
	if err != nil {
		t.Fatalf("seal password: %v", err)
	}
	return env
}

// --- tests ---

// TestWebmailSSOBridgePage_PersistsAuthStorage is the M6.2 contract
// test: the /sso/webmail handler must return an HTML page that (a)
// forwards Bulwark's Set-Cookie headers, and (b) writes Bulwark's
// zustand 'auth-storage' localStorage key with a shape that matches
// stores/auth-store.ts:partialize in Bulwark v1.4.14. A drift from
// that shape silently breaks the post-SSO bounce — keep this test
// in sync with any Bulwark pin bump.
func TestWebmailSSOBridgePage_PersistsAuthStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var ssoKey ssokey.Key
	for i := range ssoKey {
		ssoKey[i] = 0xab
	}
	plaintextPassword := "hunter2-the-quickening"

	mb := &models.Mailbox{
		ID:          "01TESTMBXID00000000000000",
		DomainID:    "01TESTDOMAINID0000000000A",
		LocalPart:   "alice",
		EmailCached: "alice@example.com",
		PasswordEnc: sealPassword(t, ssoKey, plaintextPassword),
	}
	dom := &models.Domain{ID: mb.DomainID, Name: "example.com"}

	plain, hash := mintToken(t)
	tok := &models.MailboxSSOToken{
		ID:        "01TESTTOKEN0000000000000A",
		MailboxID: mb.ID,
		UserID:    "01TESTUSER0000000000000A0",
		TokenHash: hash,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	wantServerURL := "https://mail.example.com"
	bulwark := newBulwarkFake(t, wantServerURL, mb.EmailCached)
	defer bulwark.Close()

	cfg := WebmailSSOHandlerConfig{
		Mailboxes:      &fakeMailboxRepoSSO{mb: mb},
		Domains:        &fakeDomainRepoSSO{dom: dom},
		SSOKey:         &ssoKey,
		SSOTokens:      &fakeSSOTokenRepo{tok: tok},
		BulwarkBaseURL: bulwark.URL,
	}

	r := gin.New()
	RegisterWebmailSSORoutes(r, cfg)

	req := httptest.NewRequest(http.MethodGet, "/sso/webmail?token="+plain, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// (a) 200 OK HTML, not 303.
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type: got %q, want text/html", ct)
	}

	// (b) Set-Cookie headers forwarded from Bulwark, intact.
	cookies := rec.Header().Values("Set-Cookie")
	var sawSession, sawCtx bool
	for _, c := range cookies {
		if strings.HasPrefix(c, "jmap_session=SESSIONCOOKIEVALUE") {
			sawSession = true
		}
		if strings.HasPrefix(c, "jmap_stalwart_ctx=CTXVALUE") {
			sawCtx = true
		}
	}
	if !sawSession || !sawCtx {
		t.Errorf("expected both Bulwark Set-Cookies forwarded; got %v", cookies)
	}

	// (c) Body contains localStorage write with the exact zustand shape
	// stores/auth-store.ts:partialize produces for a rememberMe:true
	// basic-auth login.
	body := rec.Body.String()
	if !strings.Contains(body, "localStorage.setItem('auth-storage'") {
		t.Errorf("body missing localStorage.setItem('auth-storage', ...); body=%s", body)
	}
	if !strings.Contains(body, "window.location.replace('/')") {
		t.Errorf("body missing window.location.replace('/'); body=%s", body)
	}

	// Pull the JSON blob out of the body and assert the shape. The
	// JSON is inlined inside a template string — html/template will
	// have double-encoded quotes, so we search for the characteristic
	// fields rather than round-trip parsing.
	required := []string{
		`\"serverUrl\":\"https://mail.example.com\"`,
		`\"username\":\"alice@example.com\"`,
		`\"authMode\":\"basic\"`,
		`\"isAuthenticated\":true`,
		`\"rememberMe\":true`,
		`\"version\":0`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Errorf("body missing required JSON marker %q; body=%s", want, body)
		}
	}
}

// TestWebmailSSO_InvalidTokenReturns403 locks in the existing failure
// path — an unknown/expired/already-consumed token returns 403, with
// no Bulwark call and no Set-Cookie leak.
func TestWebmailSSO_InvalidTokenReturns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var ssoKey ssokey.Key

	bulwark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("bulwark should not be called for invalid token")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bulwark.Close()

	cfg := WebmailSSOHandlerConfig{
		Mailboxes:      &fakeMailboxRepoSSO{},
		Domains:        &fakeDomainRepoSSO{},
		SSOKey:         &ssoKey,
		SSOTokens:      &fakeSSOTokenRepo{}, // no token seeded
		BulwarkBaseURL: bulwark.URL,
	}
	r := gin.New()
	RegisterWebmailSSORoutes(r, cfg)

	plain, _ := mintToken(t)
	req := httptest.NewRequest(http.MethodGet, "/sso/webmail?token="+plain, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rec.Code)
	}
	if len(rec.Header().Values("Set-Cookie")) > 0 {
		t.Errorf("no cookies should leak on invalid token; got %v", rec.Header().Values("Set-Cookie"))
	}
}
