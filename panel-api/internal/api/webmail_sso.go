package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// defaultBulwarkBaseURL points at the M25 unix-domain socket bulwark
// listens on. panel-api is in jabali-sockets so it can dial it. The
// `unix:` prefix follows the same convention as Kratos's admin URL
// (see kratosclient/transport.go) — operators learn one form.
const defaultBulwarkBaseURL = "unix:/run/jabali-bulwark/bulwark.sock"

// bulwarkSyntheticHost is the http.Transport-visible host used when
// dialing bulwark over UDS. Bulwark's /api/auth/session does not
// validate the Host header, so a synthetic value is fine.
const bulwarkSyntheticHost = "bulwark"

// bulwarkBridgeTmpl writes Bulwark's zustand-persist auth-storage key to
// localStorage, then navigates to `/`. Without this, Bulwark 1.4.14's
// client-side checkAuth() sees empty localStorage (even with a valid
// session cookie), defaults to unauthenticated, and redirects to
// /en/login — masking our successful server-side handoff.
//
// Shape pinned to bulwark 1.4.14 stores/auth-store.ts:partialize (the
// zustand persist middleware's default version is 0 when no `version`
// or `migrate` is configured, which 1.4.14 doesn't set). The embedded
// JSON `authStorage` value below mirrors that partialize output for a
// rememberMe:true basic-auth login, which is what our SSO flow is
// equivalent to.
//
// WARNING — BULWARK VERSION COUPLING: if the Bulwark pin in install.sh
// bumps, re-verify that stores/auth-store.ts:partialize and its persist
// config still match the shape below (name: "auth-storage", no version
// override, same field set). A silent drift here would land users back
// at /en/login on every SSO click, same as before M6.2. The test
// TestWebmailSSOBridgePage_PersistsAuthStorage asserts the wire shape.
var bulwarkBridgeTmpl = template.Must(template.New("bridge").Parse(`<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<title>Signing in…</title>
<meta http-equiv="refresh" content="0;url=/">
</head><body>
<p>Signing you in…</p>
<script>
(function () {
  try {
    localStorage.setItem('auth-storage', {{.AuthStorageJSON}});
  } catch (e) { /* private mode etc — fall through to redirect */ }
  window.location.replace('/');
})();
</script>
<noscript><p>JavaScript is required. <a href="/">Continue to webmail</a>.</p></noscript>
</body></html>
`))

// WebmailSSOHandlerConfig wires the landing endpoint that the
// mint-SSO-URL flow lands the user on. The endpoint is served under
// mail.<domain> (nginx routes /sso/webmail there from the per-domain
// mail vhost); it consumes the panel-minted token, decrypts the
// mailbox's plaintext password, POSTs Bulwark's /api/auth/session
// on the user's behalf, captures the Set-Cookie header, and 302s
// the browser to / with that cookie set.
//
// Bulwark's session-cookie encryption key is SESSION_SECRET (env:
// /etc/jabali-panel/bulwark-session.key). We DON'T touch that secret
// — Bulwark mints the cookie itself, we just pass it through. That
// means the panel never has Bulwark-session-crypto material, so a
// full DB compromise can't forge arbitrary webmail sessions.
type WebmailSSOHandlerConfig struct {
	Mailboxes repository.MailboxRepository
	Domains   repository.DomainRepository
	SSOKey    *ssokey.Key
	SSOTokens repository.MailboxSSOTokenRepository
	// BulwarkBaseURL is the URL we POST to for session creation.
	// Defaults to unix:/run/jabali-bulwark/bulwark.sock (M25 lockdown);
	// any `unix:/abs/path` form triggers a UDS-aware http.Client. Tests
	// override with an httptest server's TCP URL (no UDS rewriting).
	BulwarkBaseURL string
	// HTTPClient is mockable for tests; defaults to http.DefaultClient
	// with a 15s timeout.
	HTTPClient *http.Client
	// Log: stderr-by-default slog logger.
	Log *slog.Logger
}

// RegisterWebmailSSORoutes mounts GET /sso/webmail at the top-level
// engine root (not under /api/v1) because Bulwark / Stalwart vhosts
// don't share the API prefix and the handler is designed to be
// reached via nginx on mail.<domain>.
func RegisterWebmailSSORoutes(r gin.IRouter, cfg WebmailSSOHandlerConfig) {
	h := &webmailSSOHandler{cfg: cfg}
	r.GET("/sso/webmail", h.land)
}

type webmailSSOHandler struct{ cfg WebmailSSOHandlerConfig }

func (h *webmailSSOHandler) land(c *gin.Context) {
	ctx := c.Request.Context()

	// Refuse to act on speculative / prefetch fetches. The token is
	// single-use (consumed on first read), so a Chrome address-bar
	// prefetch or a link-rel-prefetch hit would burn the token before
	// the user's real click — they then see "token is invalid or
	// expired" even though they just landed on the URL.
	//
	// Chrome ships `Purpose: prefetch` (legacy) and `Sec-Purpose:
	// prefetch` (current spec). Either one means "do not produce
	// observable side effects". A no-content reply with cache headers
	// that forbid storage keeps the token un-consumed for the real
	// navigation.
	if isPrefetchRequest(c.Request) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Status(http.StatusNoContent)
		return
	}
	// Same idea for any responses that DO go through: tell every
	// upstream + browser intermediary that this URL must not be cached
	// or re-issued, since each successful response also consumed a
	// single-use token.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")

	token := c.Query("token")
	if token == "" {
		c.String(http.StatusBadRequest, "missing token")
		return
	}
	if h.cfg.SSOKey == nil || h.cfg.SSOTokens == nil {
		c.String(http.StatusServiceUnavailable, "webmail sso is not configured on this panel")
		return
	}

	// Hash the token before any DB work so the raw bytes never appear
	// in logs (even when the query-string is captured by upstream
	// access-log middleware).
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid token")
		return
	}
	hash := sha256.Sum256(raw)
	hashHex := hex.EncodeToString(hash[:])

	// Two-phase: PEEK now, DELETE only after bulwark confirms the
	// session was minted. Single-phase ConsumeByHash burned the token
	// before the bulwark call, so any 502 from bulwark left the user
	// with a 403 on the next retry — the recurring "token is invalid
	// or expired" loop. Phase B (delete) happens after bulwark returns
	// 200; bulwark failures keep the token live for a retry within
	// the 60s TTL.
	tok, err := h.cfg.SSOTokens.PeekByHash(ctx, hashHex)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Unknown / expired / already-consumed — all three collapse
			// to the same response so an attacker can't tell them apart.
			c.String(http.StatusForbidden, "token is invalid or expired")
			return
		}
		h.logErr("webmail sso: peek token", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	mb, err := h.cfg.Mailboxes.FindByID(ctx, tok.MailboxID)
	if err != nil {
		h.logErr("webmail sso: find mailbox", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	if len(mb.PasswordEnc) == 0 {
		c.String(http.StatusConflict, "mailbox has no SSO material; rotate the password and try again")
		return
	}
	plaintext, err := h.cfg.SSOKey.Open(mb.PasswordEnc)
	if err != nil {
		h.logErr("webmail sso: decrypt mailbox password", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	dom, err := h.cfg.Domains.FindByID(ctx, mb.DomainID)
	if err != nil {
		h.logErr("webmail sso: find domain", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	// Bulwark's /api/auth/session validates the creds against JMAP
	// before setting the session cookie, so this call is authoritative
	// — if Stalwart's SqlDirectory wouldn't accept the plaintext, we
	// get a 401 here and the user sees "Login failed" instead of an
	// inconsistent half-session.
	// serverURL must be the same value bulwark stores in its session
	// cookie AND the value the browser keeps in localStorage. Bulwark's
	// client-side checkAuth compares the two; any drift bounces the
	// user back to /en/login with "Your session has expired" — which
	// is what splitting the URL between handshake (loopback) and
	// browser (public) caused. Keep them identical here. The TLS
	// trust path for the server-side handshake fetch lives in
	// bulwark.env via NODE_TLS_REJECT_UNAUTHORIZED so self-signed
	// certs on internal mail.<domain> still resolve.
	sessionURL := h.bulwarkBase() + "/api/auth/session"
	serverURL := "https://mail." + dom.Name
	body, _ := json.Marshal(map[string]string{
		"serverUrl": serverURL,
		"username":  mb.EmailCached,
		"password":  string(plaintext),
	})
	// Guard against plaintext lingering on the stack longer than
	// necessary — after Marshal, the bytes are inside `body`. We can't
	// force a wipe in Go, but this is still the narrow window.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL, bytes.NewReader(body))
	if err != nil {
		h.logErr("webmail sso: build bulwark request", err)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient().Do(req)
	if err != nil {
		h.logErr("webmail sso: bulwark call", err)
		c.String(http.StatusBadGateway, "webmail is unreachable; try again in a moment")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Bulwark rejected the creds — probably Stalwart auth failed
		// because the mailbox was disabled or password rotated out of
		// band. Surface a user-facing hint.
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		c.String(http.StatusBadGateway, "webmail login failed (status %d); try logging in manually", resp.StatusCode)
		return
	}

	// Bulwark accepted the creds — burn the SSO token now (phase B of
	// the two-phase consume). Idempotent on the repo side so a
	// concurrent prefetch-then-real-click race doesn't 500 the
	// success path.
	if err := h.cfg.SSOTokens.DeleteByHash(ctx, hashHex); err != nil {
		h.logErr("webmail sso: delete token after success", err)
		// Soft-fail: bulwark already minted a session, the user is
		// effectively logged in. Best-effort cleanup; the 60s TTL
		// PurgeExpired sweep is the backstop.
	}

	// Forward Bulwark's Set-Cookie headers onto our response. Bulwark
	// sets an httpOnly session cookie; we want the browser to receive
	// it under the same origin (mail.<domain>) so the subsequent
	// navigation to `/` lands with the user authenticated.
	setCount := 0
	for _, sc := range resp.Header.Values("Set-Cookie") {
		c.Writer.Header().Add("Set-Cookie", sc)
		setCount++
	}
	if setCount == 0 {
		h.logErr("webmail sso: bulwark returned no Set-Cookie", errors.New("empty"))
		c.String(http.StatusBadGateway, "webmail did not set a session cookie; try logging in manually")
		return
	}

	// Render a bridge page that writes Bulwark's zustand auth-storage
	// localStorage key before navigating to `/`. The plain 303 we used
	// before M6.2 set the session cookie correctly but Bulwark's
	// client-side checkAuth() still bounced to /en/login because it
	// keys its "am I logged in?" decision off localStorage, not the
	// cookie. See bulwarkBridgeTmpl for the exact shape + version pin.
	authStorageBytes, mErr := json.Marshal(map[string]any{
		"state": map[string]any{
			"serverUrl":        serverURL,
			"username":         mb.EmailCached,
			"authMode":         "basic",
			"isAuthenticated":  true,
			"rememberMe":       true,
			"activeAccountId":  nil,
		},
		"version": 0,
	})
	if mErr != nil {
		h.logErr("webmail sso: marshal auth-storage", mErr)
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	// html/template's context-aware escaping handles the script tag
	// correctly: `template.JS` wrapping would inline the JSON raw, but
	// string context inside a script auto-escapes dangerous sequences
	// like `</script>`. We want the JSON string *as a quoted JS string*
	// so localStorage.setItem receives a single argument — hence
	// embedding `{{.AuthStorageJSON}}` as a string field and letting
	// template escape it into a JS string literal.
	if err := bulwarkBridgeTmpl.Execute(c.Writer, map[string]any{
		"AuthStorageJSON": string(authStorageBytes),
	}); err != nil {
		h.logErr("webmail sso: render bridge", err)
	}
}

// bulwarkBase returns the base URL we POST to for Bulwark session
// creation. For `unix:/abs/path` configs (the default) we collapse to
// the synthetic `http://bulwark` host so net/url.Parse and the http
// client's Host-header logic still work — the per-request transport
// (built in httpClient) routes that synthetic host over the socket.
func (h *webmailSSOHandler) bulwarkBase() string {
	raw := h.cfg.BulwarkBaseURL
	if raw == "" {
		raw = defaultBulwarkBaseURL
	}
	if strings.HasPrefix(raw, "unix:") {
		return "http://" + bulwarkSyntheticHost
	}
	return strings.TrimSuffix(raw, "/")
}

func (h *webmailSSOHandler) httpClient() *http.Client {
	if h.cfg.HTTPClient != nil {
		return h.cfg.HTTPClient
	}
	raw := h.cfg.BulwarkBaseURL
	if raw == "" {
		raw = defaultBulwarkBaseURL
	}
	if !strings.HasPrefix(raw, "unix:") {
		return &http.Client{Timeout: 15 * time.Second}
	}
	sockPath := strings.TrimPrefix(strings.TrimPrefix(raw, "unix:"), "//")
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return dialer.DialContext(ctx, network, addr)
			}
			if host == bulwarkSyntheticHost {
				return dialer.DialContext(ctx, "unix", sockPath)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: tr}
}

// isPrefetchRequest reports whether the inbound request was issued by
// a speculative prefetch (Chrome address-bar prerender, <link
// rel=prefetch>, etc) rather than a real navigation. Chrome stamps
// `Sec-Purpose: prefetch` (current spec) or the legacy `Purpose:
// prefetch`. We match both, case-insensitive, on a substring so future
// values like `prefetch;prerender` still hit.
func isPrefetchRequest(r *http.Request) bool {
	for _, h := range []string{"Sec-Purpose", "Purpose"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		if strings.Contains(strings.ToLower(v), "prefetch") {
			return true
		}
		if strings.Contains(strings.ToLower(v), "prerender") {
			return true
		}
	}
	return false
}

func (h *webmailSSOHandler) logErr(msg string, err error) {
	log := h.cfg.Log
	if log == nil {
		slog.Error(msg, "err", err)
		return
	}
	log.Error(msg, "err", err)
}

// webmailSSOBuildRequestContext is a test-only hook; kept here to
// silence "unused" lints when someone removes the helper later.
var _ = fmt.Sprintf
var _ = context.Background
