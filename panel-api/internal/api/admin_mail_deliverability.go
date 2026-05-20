// Package api — M47 Wave 9 admin deliverability score card.
//
// Aggregates the signals every other M47 wave persists:
//   - Wave 5 mail_rbl_state: any blacklisting of the public IP
//   - Wave 6 dmarc_aggregate: DKIM-failing-bucket count (last 7d)
//   - Wave 4 arf_report: abuse-feedback rate (last 7d)
//   - Wave 8 tlsrpt_aggregate: TLS session failures (last 7d)
//
// Computes a single 0-100 score (100 = clean) the admin Mail panel
// surfaces as a colour badge. Each component is a 0-25 deduction so
// the operator can see exactly which signal cost what.
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type AdminMailDeliverabilityHandlerConfig struct {
	MailRBLStates    repository.MailRBLStateRepository
	DMARCAggregate   repository.DMARCAggregateRepository
	TLSRPTAggregate  repository.TLSRPTAggregateRepository
	ARFReports       repository.ARFReportRepository
	ServerSettings   repository.ServerSettingsRepository
}

func RegisterAdminMailDeliverabilityRoutes(g *gin.RouterGroup, cfg AdminMailDeliverabilityHandlerConfig) {
	h := &adminMailDeliverabilityHandler{cfg: cfg}
	grp := g.Group("/admin/mail")
	grp.Use(middleware.RequireAdmin())
	grp.GET("/deliverability", h.score)
}

type adminMailDeliverabilityHandler struct {
	cfg AdminMailDeliverabilityHandlerConfig
}

type deliverabilityComponent struct {
	Name        string `json:"name"`
	Value       int64  `json:"value"`
	Deduction   int    `json:"deduction"`
	Detail      string `json:"detail"`
}

type deliverabilityResponse struct {
	Score       int                       `json:"score"`        // 0-100, 100 = clean
	Severity    string                    `json:"severity"`     // ok|warning|critical
	GeneratedAt time.Time                 `json:"generated_at"`
	Domain      string                    `json:"domain,omitempty"` // empty = server-wide
	Components  []deliverabilityComponent `json:"components"`
}

func (h *adminMailDeliverabilityHandler) score(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now().UTC()
	since := now.Add(-7 * 24 * time.Hour)

	// Optional ?domain= narrows DMARC/TLS-RPT buckets to one domain --
	// the per-domain widget (Wave 9b). Server-wide score is the default
	// when absent. RBL stays server-wide regardless (the IP is shared
	// across all hosted domains). ARF is filtered by original_mail_from
	// match when present.
	domain := strings.TrimSpace(c.Query("domain"))

	resp := deliverabilityResponse{Score: 100, GeneratedAt: now, Domain: domain}

	// RBL: each listing burns 25; zero if no RBL repo wired.
	if domain == "" && h.cfg.MailRBLStates != nil && h.cfg.ServerSettings != nil {
		if s, err := h.cfg.ServerSettings.Get(ctx); err == nil && s != nil && s.PublicIPv4 != "" {
			rows, _ := h.cfg.MailRBLStates.ListByIP(ctx, s.PublicIPv4)
			listed := int64(0)
			for _, r := range rows {
				if r.Listed {
					listed++
				}
			}
			ded := componentDed(int(listed), 1, 25)
			resp.Components = append(resp.Components, deliverabilityComponent{
				Name: "rbl", Value: listed, Deduction: ded,
				Detail: "Listings on curated RBLs (Spamhaus/SpamCop/Barracuda/SURBL).",
			})
			resp.Score -= ded
		}
	}
	// DMARC: failing-DKIM bucket count last 7d, normalised per-bucket.
	// We don't have a per-domain narrow here (admin score is server-wide)
	// so query the wildcard via empty string — repo treats empty as
	// "any". Implementation: callers can scope per domain in a future
	// per-domain widget; v1 is server-wide.
	if h.cfg.DMARCAggregate != nil {
		failed, _ := h.cfg.DMARCAggregate.CountFailuresSince(ctx, domain, since)
		ded := componentDed(int(failed), 5, 25)
		resp.Components = append(resp.Components, deliverabilityComponent{
			Name: "dmarc_dkim_failures", Value: failed, Deduction: ded,
			Detail: "DKIM-failing record buckets in inbound DMARC RUA reports (last 7d).",
		})
		resp.Score -= ded
	}
	// TLS-RPT: failure session count last 7d.
	if h.cfg.TLSRPTAggregate != nil {
		failed, _ := h.cfg.TLSRPTAggregate.CountFailuresSince(ctx, domain, since)
		ded := componentDed(int(failed), 10, 25)
		resp.Components = append(resp.Components, deliverabilityComponent{
			Name: "tlsrpt_failures", Value: failed, Deduction: ded,
			Detail: "Failed TLS sessions reported via TLS-RPT (last 7d).",
		})
		resp.Score -= ded
	}
	// ARF: abuse-feedback report count last 7d.
	if h.cfg.ARFReports != nil {
		count, _ := h.cfg.ARFReports.CountForDomainSince(ctx, domain, since)
		ded := componentDed(int(count), 1, 25)
		resp.Components = append(resp.Components, deliverabilityComponent{
			Name: "abuse_reports", Value: count, Deduction: ded,
			Detail: "Abuse-feedback reports from receiver postmasters (last 7d).",
		})
		resp.Score -= ded
	}
	if resp.Score < 0 {
		resp.Score = 0
	}
	switch {
	case resp.Score >= 90:
		resp.Severity = "ok"
	case resp.Score >= 60:
		resp.Severity = "warning"
	default:
		resp.Severity = "critical"
	}
	c.JSON(http.StatusOK, resp)
}

// componentDed maps a raw count to a 0..max deduction in `step` units.
//
//	value=0   → 0
//	value=1   → step
//	value=2   → 2*step
//	... capped at max.
func componentDed(value, perUnit, max int) int {
	if value <= 0 || perUnit <= 0 {
		return 0
	}
	d := value * perUnit
	if d > max {
		return max
	}
	return d
}
