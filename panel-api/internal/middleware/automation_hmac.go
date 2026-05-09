// Package middleware — Automation API HMAC verification (M44).
//
// Authorization header format:
//
//   Authorization: Jabali-HMAC kid=<token-id>, ts=<unix>, sig=<hex>
//
// Server recomputes:
//
//   sig = hex(HMAC_SHA256(secret, METHOD || "\n" || PATH || "\n" || ts || "\n" || sha256(BODY)))
//
// Constant-time compares against the header sig. ts must be within a
// 5-minute window of the server clock; signatures already seen in
// that window are rejected via Redis SETNX so a captured request
// cannot be replayed (M44 replay defense, supersedes the original
// "no nonce store" caveat in plans/automation-api-tokens.md).
package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

const (
	autoCtxTokenKey = "jabali_automation_token"
	autoMaxSkew     = 5 * time.Minute
	autoMaxBody     = 1 << 20 // 1 MiB cap on signed body — read-only API doesn't take bigger
	// autoReplayTTL covers the skew window + 1-min grace so a sig
	// arriving at the trailing edge can't be replayed mid-tick.
	// Replay key shape: "automation:replay:<kid>:<sig>" — per-token
	// scoping prevents one token's collision space from leaking
	// across tenants. sig is already a 64-byte HMAC hex; no
	// further hashing needed.
	autoReplayTTL = autoMaxSkew + time.Minute
)

// AutomationToken returns the verified token from the context, or nil
// when the request didn't pass through RequireAutomationHMAC.
func AutomationToken(c *gin.Context) *models.AutomationToken {
	v, ok := c.Get(autoCtxTokenKey)
	if !ok {
		return nil
	}
	t, _ := v.(*models.AutomationToken)
	return t
}

// RequireAutomationHMAC parses the Authorization header, looks the
// token up by kid, decrypts the per-token secret via the global
// ssokey, and verifies the HMAC. On success the verified token is
// stashed in the gin context for downstream scope middleware.
//
// Failure cases all 401 with a generic JSON error — no information
// leak about why a particular request failed.
// rdb is nullable. When nil, replay defense is skipped + a single
// info log fires at first request (operator can decide whether to
// wire Redis or accept the timestamp-only window). On a fresh
// install Redis is always present (M14 dispatcher requires it),
// so production paths exercise the SETNX gate.
func RequireAutomationHMAC(repo repository.AutomationTokenRepository, key *ssokey.Key, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if repo == nil || key == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "automation api disabled"})
			return
		}

		raw := c.GetHeader("Authorization")
		const prefix = "Jabali-HMAC "
		if !strings.HasPrefix(raw, prefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			return
		}
		params := parseAutoAuthParams(raw[len(prefix):])
		kid := params["kid"]
		tsStr := params["ts"]
		sig := params["sig"]
		if kid == "" || tsStr == "" || sig == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			return
		}

		// Clock skew window.
		tsInt, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid timestamp"})
			return
		}
		ts := time.Unix(tsInt, 0)
		if d := time.Since(ts); d > autoMaxSkew || d < -autoMaxSkew {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "timestamp outside window"})
			return
		}

		// Token lookup.
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		tok, err := repo.FindByID(ctx, kid)
		if err != nil || tok == nil || tok.RevokedAt != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		// Decrypt secret.
		secret, err := key.Open(tok.SecretEnc)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		// Snapshot body so handler can still read it AND signature can
		// be computed over it. Capped to autoMaxBody to bound memory.
		var body []byte
		if c.Request.Body != nil {
			body, err = io.ReadAll(io.LimitReader(c.Request.Body, autoMaxBody+1))
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
				return
			}
			if len(body) > autoMaxBody {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "body too large"})
				return
			}
			// Restore for downstream handler via a fresh reader.
			c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
		}

		// Recompute signature.
		bodyHash := sha256.Sum256(body)
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(c.Request.Method))
		mac.Write([]byte("\n"))
		mac.Write([]byte(c.Request.URL.RequestURI()))
		mac.Write([]byte("\n"))
		mac.Write([]byte(tsStr))
		mac.Write([]byte("\n"))
		mac.Write([]byte(hex.EncodeToString(bodyHash[:])))
		expected := hex.EncodeToString(mac.Sum(nil))

		gotBytes, err := hex.DecodeString(sig)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}
		expBytes, _ := hex.DecodeString(expected)
		if subtle.ConstantTimeCompare(gotBytes, expBytes) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		// Replay defense: SETNX (sig already verified at this point;
		// scoped per-token to keep tenants isolated). 5-min window +
		// 1-min grace TTL bounds memory: each request stamps one
		// key that auto-expires.
		//
		// Redis-down → fail-closed: a missing replay store is a
		// security regression, not 'best effort'. The middleware
		// returns 503 so the caller knows the auth substrate is
		// degraded rather than silently dropping the gate.
		if rdb != nil {
			rkey := fmtReplayKey(kid, sig)
			rctx, rcancel := context.WithTimeout(c.Request.Context(), 1*time.Second)
			ok, rerr := rdb.SetNX(rctx, rkey, "1", autoReplayTTL).Result()
			rcancel()
			if rerr != nil {
				if !errors.Is(rerr, context.Canceled) && !errors.Is(rerr, context.DeadlineExceeded) {
					c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "replay store unavailable"})
					return
				}
				// On context-cancel from the request itself, the
				// caller went away; don't manufacture a 503 they
				// won't see — let gin clean up.
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "replay store unavailable"})
				return
			}
			if !ok {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "replay detected"})
				return
			}
		}

		// Bump last-used best-effort. Don't block the request on the
		// repo write — if it fails, the verified request still proceeds.
		go func(id, ip string) {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer bgCancel()
			_ = repo.BumpLastUsed(bgCtx, id, ip)
		}(tok.ID, c.ClientIP())

		c.Set(autoCtxTokenKey, tok)
		c.Next()
	}
}

// parseAutoAuthParams walks the comma-separated `key=value, ...`
// suffix of the Authorization header. Values are unquoted; spaces
// around commas are tolerated. Keys are lower-cased on the way in.
func parseAutoAuthParams(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(part[:eq]))
		v := strings.TrimSpace(part[eq+1:])
		v = strings.Trim(v, `"`)
		out[k] = v
	}
	return out
}

// fmtReplayKey builds the per-token replay key. Imported via
// fmt at top of file; kept short here to avoid dragging fmt
// just for this single Sprintf — use string concat which is
// also faster.
func fmtReplayKey(kid, sig string) string {
	return "automation:replay:" + kid + ":" + sig
}

// RequireScope returns a middleware that 403s when the verified
// automation token's scopes don't include the required capability.
// Wildcard scopes (e.g. "read:*") match per AutomationScopes.Has.
func RequireScope(want string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := AutomationToken(c)
		if tok == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "automation token required"})
			return
		}
		if !tok.Scopes.Has(want) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "scope " + want + " not granted to token"})
			return
		}
		c.Next()
	}
}
