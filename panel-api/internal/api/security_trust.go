// security_trust.go — M43 Step 7. Single test-bench endpoint that
// returns every brain's verdict for a given IP. Lets the admin
// answer "would this IP be blocked?" in one request without
// reproducing traffic.
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// RegisterSecurityTrustRoutes mounts the M43 Trust admin endpoints.
// Read-only — no decisions are created or modified.
//
// Routes through the agent over the M25 unix socket. panel-api runs
// as the jabali user; cscli reads /etc/crowdsec/config.yaml (root)
// and ufw needs root for netfilter inspect, so direct exec from
// panel-api EACCES'd every call and the trust bench was useless.
// The agent's security.trust.test handler runs the same probes as
// root and returns the structured verdict list.
func RegisterSecurityTrustRoutes(rg *gin.RouterGroup, cli agent.AgentInterface) {
	g := rg.Group("/admin/security/trust", middleware.RequireAdmin())

	// POST /admin/security/trust/test  {ip}
	// Returns:
	//   {
	//     "ip": "1.2.3.4",
	//     "verdicts": [
	//       {"layer":"crowdsec","outcome":"allow|deny|unknown","detail":"…"},
	//       {"layer":"ufw","outcome":"allow|deny|unknown","detail":"…"}
	//     ]
	//   }
	g.POST("/test", func(c *gin.Context) {
		var body struct {
			IP string `json:"ip"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.IP) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ip required"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		raw, err := cli.Call(ctx, "security.trust.test", map[string]string{
			"ip": strings.TrimSpace(body.IP),
		})
		if err != nil {
			status, errBody := translateAgentError(err)
			c.JSON(status, errBody)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
	})
}
