package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// M26 Step 4 (ADR-0053). CrowdSec admin endpoints.

const csCallTimeout = 10 * time.Second

// validCrowdSecScopes mirrors the agent-side allow-list. Reject unknown
// scope values at the API edge so the operator gets a clean 400 instead
// of a generic agent error. See feedback_verify_wire_contract — keep
// the wire-contract values explicit.
var validCrowdSecScopes = map[string]bool{
	"ip": true, "range": true, "country": true, "as": true,
}

// RegisterSecurityCrowdSecRoutes mounts admin-only CrowdSec endpoints.
func RegisterSecurityCrowdSecRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/crowdsec", middleware.RequireAdmin())

	g.GET("/status", agentPassthrough(cli, "security.crowdsec.status", nil, csCallTimeout))

	g.GET("/decisions", func(c *gin.Context) {
		params := map[string]any{}
		if scope := c.Query("scope"); scope != "" {
			if !validCrowdSecScopes[scope] {
				c.JSON(http.StatusBadRequest, gin.H{
					"status": "error",
					"error":  "invalid_scope",
					"detail": "scope must be one of ip|range|country|as",
				})
				return
			}
			params["scope"] = scope
		}
		if limit := c.Query("limit"); limit != "" {
			n, err := strconv.Atoi(limit)
			if err != nil || n < 1 || n > 1000 {
				c.JSON(http.StatusBadRequest, gin.H{
					"status": "error",
					"error":  "invalid_limit",
				})
				return
			}
			params["limit"] = n
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.decisions.list", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.POST("/decisions", func(c *gin.Context) {
		var body struct {
			Scope    string `json:"scope"`
			Value    string `json:"value"`
			Duration string `json:"duration"`
			Reason   string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.decisions.add", map[string]any{
			"scope":    body.Scope,
			"value":    body.Value,
			"duration": body.Duration,
			"reason":   body.Reason,
		})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusCreated, "application/json; charset=utf-8", raw)
	})

	g.DELETE("/decisions/:id", func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_id"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.decisions.delete", map[string]any{"id": id})
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	g.GET("/bouncers", agentPassthrough(cli, "security.crowdsec.bouncers.list", nil, csCallTimeout))
	g.GET("/metrics", agentPassthrough(cli, "security.crowdsec.metrics", nil, csCallTimeout))

	g.GET("/hub", func(c *gin.Context) {
		params := map[string]any{}
		if t := c.Query("type"); t != "" {
			params["type"] = t
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.hub.list", params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})

	// M27 Step 2 — allowlists (ADR-0061). Wire contract:
	//   GET    /admin/security/crowdsec/allowlists           → {items}
	//   POST   /admin/security/crowdsec/allowlists           {value, reason}
	//   DELETE /admin/security/crowdsec/allowlists?value=... (query param; CIDR has `/` which :path would segment)
	g.GET("/allowlists", agentPassthrough(cli, "security.crowdsec.allowlists.list", nil, csCallTimeout))

	g.POST("/allowlists", func(c *gin.Context) {
		var body struct {
			Value  string `json:"value"`
			Reason string `json:"reason"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		body.Value = strings.TrimSpace(body.Value)
		if body.Value == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "value_required"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		raw, err := cli.Call(ctx, "security.crowdsec.allowlists.add", body)
		if err != nil {
			status, ebody := translateAgentError(err)
			c.JSON(status, ebody)
			return
		}
		c.Data(http.StatusCreated, "application/json; charset=utf-8", raw)
	})

	g.DELETE("/allowlists", func(c *gin.Context) {
		value := strings.TrimSpace(c.Query("value"))
		if value == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "value_required"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		_, err := cli.Call(ctx, "security.crowdsec.allowlists.remove", map[string]any{"value": value})
		if err != nil {
			status, ebody := translateAgentError(err)
			c.JSON(status, ebody)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// RegisterSecurityAppSecRoutes mounts the admin AppSec-geoblock surface.
// Split from RegisterSecurityCrowdSecRoutes because it needs the
// ServerSettings repo (DB is the source of truth; agent rewrites the
// YAML rule on every set). When settings is nil the endpoints are
// intentionally skipped so tests that only stub the agent don't need to
// bring up the full repo graph.
func RegisterSecurityAppSecRoutes(rg *gin.RouterGroup, cli agent.AgentInterface, settings repository.ServerSettingsRepository) {
	if settings == nil {
		return
	}
	g := rg.Group("/admin/security/crowdsec/appsec", middleware.RequireAdmin())

	g.GET("/geoblock", func(c *gin.Context) {
		s, err := settings.Get(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "error": "settings_read", "detail": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, appsecGeoblockResponse(s))
	})

	g.PUT("/geoblock", func(c *gin.Context) {
		var body struct {
			Mode      string   `json:"mode"`
			Countries []string `json:"countries"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_json"})
			return
		}
		if _, ok := appsecGeoblockModes[body.Mode]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error", "error": "invalid_mode",
				"detail": "mode must be off|allow|deny",
			})
			return
		}
		cleaned, errResp := cleanCountries(body.Countries)
		if errResp != nil {
			c.JSON(http.StatusBadRequest, errResp)
			return
		}
		if (body.Mode == "allow" || body.Mode == "deny") && len(cleaned) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"status": "error", "error": "empty_countries",
				"detail": "mode " + body.Mode + " requires at least one country",
			})
			return
		}
		// Dispatch agent first so a failure to rewrite the YAML rule
		// doesn't leave the DB drifted from the filesystem.
		ctx, cancel := context.WithTimeout(c.Request.Context(), csCallTimeout)
		defer cancel()
		if _, err := cli.Call(ctx, "security.crowdsec.appsec.geoblock.set", map[string]any{
			"mode":      body.Mode,
			"countries": cleaned,
		}); err != nil {
			status, errBody := translateAgentError(err)
			c.JSON(status, errBody)
			return
		}
		s, err := settings.Get(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "error": "settings_read", "detail": err.Error(),
			})
			return
		}
		s.AppSecGeoblockMode = body.Mode
		s.AppSecGeoblockCountries = strings.Join(cleaned, ",")
		if err := settings.Upsert(c.Request.Context(), s); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error", "error": "settings_write", "detail": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, appsecGeoblockResponse(s))
	})
}

// appsecGeoblockModes mirrors the agent + CONVENTIONS list. Duplicated
// at the API edge so bad input gets a 400 without a round-trip.
var appsecGeoblockModes = map[string]struct{}{
	"off": {}, "allow": {}, "deny": {},
}

var appsecCountryCodeRE = regexp.MustCompile(`^[A-Z]{2}$`)

func appsecGeoblockResponse(s *models.ServerSettings) gin.H {
	countries := []string{}
	if s.AppSecGeoblockCountries != "" {
		countries = strings.Split(s.AppSecGeoblockCountries, ",")
	}
	return gin.H{
		"mode":      s.AppSecGeoblockMode,
		"countries": countries,
	}
}

func cleanCountries(in []string) ([]string, gin.H) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, c := range in {
		code := strings.ToUpper(strings.TrimSpace(c))
		if code == "" {
			continue
		}
		if !appsecCountryCodeRE.MatchString(code) {
			return nil, gin.H{
				"status": "error", "error": "invalid_country",
				"detail": "country " + c + " must be a 2-letter ISO 3166-1 code",
			}
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out, nil
}

// agentPassthrough is a shared helper for "GET that just forwards to a
// no-arg agent command and returns the raw JSON response."
func agentPassthrough(cli agent.AgentInterface, command string, params any, timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		raw, err := cli.Call(ctx, command, params)
		if err != nil {
			status, body := translateAgentError(err)
			c.JSON(status, body)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	}
}
