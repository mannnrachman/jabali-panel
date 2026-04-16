package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

type cliLoginRequest struct {
	CLIToken string `json:"cli_token" binding:"required"`
}

func (h *authHandler) cliLogin(c *gin.Context) {
	var req cliLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	deviceID := auth.DeriveDeviceID(
		c.GetHeader("X-Device-Id"),
		c.Request.UserAgent(),
		c.ClientIP(),
	)

	out, err := h.cfg.Service.RedeemCLIToken(c.Request.Context(), req.CLIToken, deviceID)
	if err != nil {
		// Always return 401 for any error (token invalid, expired, wrong purpose, user not admin, etc)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_cli_token"})
		return
	}

	// Only set refresh cookie if RawRefresh is not empty (non-impersonation sessions).
	// Impersonation tokens return empty RawRefresh to signal a one-shot tab.
	if out.RawRefresh != "" {
		h.setRefreshCookie(c, out.RawRefresh)
	}
	c.JSON(http.StatusOK, h.buildLoginResponse(out))
}
