package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/audit"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// isMutating — only state-changing requests are audited (honest v1
// scope per ADR-0105: NOT every GET).
func isMutating(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// AuditRecord returns middleware that records one generic API-mutation
// audit event AFTER the handler runs. It NEVER reads the request body
// (the secret-leak scar) — only the HTTP method + route *template*
// (no high-cardinality ids), the actor (ginctx claims), the result
// (mapped from the response status), source IP and request id, plus a
// best-effort informational target from path params.
//
// rec is fire-and-forget by contract (audit.Recorder). A nil rec
// disables auditing (Redis-less boot / tests) — the middleware is
// then a pure pass-through.
func AuditRecord(rec audit.Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rec == nil || !isMutating(c.Request.Method) {
			c.Next()
			return
		}
		c.Next()

		// Route TEMPLATE, e.g. "/api/v1/users/:id" — never the
		// concrete URL, so `action` stays low-cardinality. Empty =>
		// unmatched route (404/no handler); nothing to attribute.
		route := c.FullPath()
		if route == "" {
			return
		}

		result := models.AuditResultError
		switch s := c.Writer.Status(); {
		case s >= 200 && s < 300:
			result = models.AuditResultOK
		case s == http.StatusUnauthorized, s == http.StatusForbidden:
			result = models.AuditResultDenied
		}

		var actorUserID, actorKind string
		if cl := ginctx.Claims(c); cl != nil {
			actorUserID = cl.UserID
			if cl.IsAdmin {
				actorKind = models.AuditActorAdmin
			} else {
				actorKind = models.AuditActorUser
			}
		} else {
			actorKind = models.AuditActorSystem
		}

		// Subject is attributed generically ONLY for self-scoped
		// /api/v1/me/* routes (subject = actor). Cross-user
		// attribution is the job of the explicit Step-3 domain
		// emitters that know the semantics — treating a generic ":id"
		// as a user id would mis-attribute (it is usually a
		// domain/db/token id). Honest v1 scope; a wrong subject is
		// worse than an absent one (absent = admin-only visibility,
		// the safe default).
		subject := ""
		if strings.HasPrefix(route, "/api/v1/me") {
			subject = actorUserID
		}

		targetType, targetID := deriveTarget(route, c)

		rec.Record(audit.APIMutation(
			actorUserID, actorKind, subject,
			c.Request.Method+" "+route,
			targetType, targetID, result,
			clientIP(c), ginctx.RequestID(c),
		))
	}
}

// clientIP resolves the real caller IP for the audit row.
//
// panel-api is reachable ONLY through the local nginx over a unix
// socket (/run/jabali-panel/api.sock). For a unix-socket peer Go sets
// RemoteAddr to "@"/"" — gin can't parse it as an IP, so its trusted-
// proxy gate never consults the forwarded headers and c.ClientIP()
// returns "" (every audit row then showed "—"). nginx sets X-Real-IP
// to $remote_addr — the actual TCP peer, proxy-authoritative and NOT
// client-spoofable given the socket-only bind. Prefer it, then the
// first X-Forwarded-For hop, then gin's value as a last resort.
func clientIP(c *gin.Context) string {
	if xr := strings.TrimSpace(c.GetHeader("X-Real-IP")); xr != "" {
		return xr
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if xff = strings.TrimSpace(xff); xff != "" {
			return xff
		}
	}
	return c.ClientIP()
}

// deriveTarget is best-effort and informational only (target is not a
// security control). It takes the coarse resource word after /api/v1/
// and the conventional ":id" path param when present.
func deriveTarget(route string, c *gin.Context) (targetType, targetID string) {
	targetID = c.Param("id")
	seg := strings.SplitN(strings.TrimPrefix(route, "/api/v1/"), "/", 2)[0]
	seg = strings.TrimPrefix(seg, "admin/") // /api/v1/admin/<thing>
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	targetType = strings.TrimSuffix(seg, "s")
	if targetType == "" {
		targetType = "api"
	}
	return targetType, targetID
}
