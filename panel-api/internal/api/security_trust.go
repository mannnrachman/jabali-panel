// security_trust.go — M43 Step 7. Single test-bench endpoint that
// returns every brain's verdict for a given IP. Lets the admin
// answer "would this IP be blocked?" in one request without
// reproducing traffic.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// RegisterSecurityTrustRoutes mounts the M43 Trust admin endpoints.
// Read-only — no decisions are created or modified.
func RegisterSecurityTrustRoutes(rg *gin.RouterGroup, _ agent.AgentInterface) {
	g := rg.Group("/admin/security/trust", middleware.RequireAdmin())

	// POST /admin/security/trust/test  {ip}
	// Returns:
	//   {
	//     "ip": "1.2.3.4",
	//     "verdicts": [
	//       {"layer":"crowdsec","outcome":"allow|deny","detail":"…"},
	//       {"layer":"ufw","outcome":"allow|deny","detail":"…"}
	//     ]
	//   }
	g.POST("/test", func(c *gin.Context) {
		var body struct {
			IP string `json:"ip"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.IP == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ip required"})
			return
		}
		ip := strings.TrimSpace(body.IP)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		verdicts := []map[string]string{
			testCrowdSecVerdict(ctx, ip),
			testUfwVerdict(ctx, ip),
		}
		c.JSON(http.StatusOK, gin.H{
			"ip":       ip,
			"verdicts": verdicts,
		})
	})
}

func testCrowdSecVerdict(ctx context.Context, ip string) map[string]string {
	if _, err := exec.LookPath("cscli"); err != nil {
		return map[string]string{"layer": "crowdsec", "outcome": "unknown", "detail": "cscli not installed"}
	}
	out, err := exec.CommandContext(ctx, "cscli", "decisions", "list", "-i", ip, "-o", "json").Output()
	if err != nil {
		return map[string]string{"layer": "crowdsec", "outcome": "unknown", "detail": "cscli error: " + err.Error()}
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return map[string]string{"layer": "crowdsec", "outcome": "allow", "detail": "no active decision"}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return map[string]string{"layer": "crowdsec", "outcome": "unknown", "detail": "parse: " + err.Error()}
	}
	if len(rows) == 0 {
		return map[string]string{"layer": "crowdsec", "outcome": "allow", "detail": "no active decision"}
	}
	return map[string]string{
		"layer":   "crowdsec",
		"outcome": "deny",
		"detail":  "active decision(s) present (count=" + itoa(len(rows)) + ")",
	}
}

func testUfwVerdict(ctx context.Context, ip string) map[string]string {
	if _, err := exec.LookPath("ufw"); err != nil {
		return map[string]string{"layer": "ufw", "outcome": "unknown", "detail": "ufw not installed"}
	}
	out, err := exec.CommandContext(ctx, "ufw", "status", "numbered").Output()
	if err != nil {
		return map[string]string{"layer": "ufw", "outcome": "unknown", "detail": "ufw error"}
	}
	// Naive substring match — detects both bare IPs and CIDR strings
	// that contain the IP. Good enough for the test bench; a real
	// CIDR contains-check belongs in a dedicated helper if this grows.
	if strings.Contains(string(out), ip) {
		return map[string]string{
			"layer":   "ufw",
			"outcome": "deny",
			"detail":  "matching UFW rule found (M43 Step 4 candidate — migrate to CrowdSec)",
		}
	}
	return map[string]string{"layer": "ufw", "outcome": "allow", "detail": "no matching UFW rule"}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
