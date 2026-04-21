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
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

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
	// BulwarkBaseURL is the internal loopback URL we POST to for
	// session creation. Defaults to http://127.0.0.1:3000. Tests
	// override this to point at an httptest server.
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

	tok, err := h.cfg.SSOTokens.ConsumeByHash(ctx, hashHex)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Unknown / expired / already-consumed — all three collapse
			// to the same response so an attacker can't tell them apart.
			c.String(http.StatusForbidden, "token is invalid or expired")
			return
		}
		h.logErr("webmail sso: consume token", err)
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

	// Forward Bulwark's Set-Cookie headers onto our response. Bulwark
	// sets an httpOnly session cookie; we want the browser to receive
	// it under the same origin (mail.<domain>) so the subsequent 302
	// lands with the user authenticated.
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

	// 303 See Other — explicit "make the next request GET /" so
	// browsers don't replay the POST-ish semantics of the landing.
	c.Redirect(http.StatusSeeOther, "/")
}

func (h *webmailSSOHandler) bulwarkBase() string {
	if h.cfg.BulwarkBaseURL != "" {
		return strings.TrimSuffix(h.cfg.BulwarkBaseURL, "/")
	}
	return "http://127.0.0.1:3000"
}

func (h *webmailSSOHandler) httpClient() *http.Client {
	if h.cfg.HTTPClient != nil {
		return h.cfg.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
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
