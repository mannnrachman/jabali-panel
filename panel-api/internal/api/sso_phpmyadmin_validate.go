package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// SSOPhpMyAdminValidateHandlerConfig plugs the UDS validate handler into the router.
type SSOPhpMyAdminValidateHandlerConfig struct {
	Databases repository.DatabaseRepository
	Users     repository.UserRepository
	Tokens    repository.PhpMyAdminSSOTokenRepository
	SSOKey    *ssokey.Key
	Log       *slog.Logger
}

// RegisterSSOPhpMyAdminValidateRoutes mounts POST /sso/phpmyadmin/validate
// on the given router (typically a UDS listener, no JWT auth).
func RegisterSSOPhpMyAdminValidateRoutes(g *gin.RouterGroup, cfg SSOPhpMyAdminValidateHandlerConfig) {
	h := &ssoPhpMyAdminValidateHandler{cfg: cfg}
	g.POST("/sso/phpmyadmin/validate", h.validate)
}

type ssoPhpMyAdminValidateHandler struct {
	cfg SSOPhpMyAdminValidateHandlerConfig
}

type ssoValidateRequest struct {
	Token string `json:"token" binding:"required"`
}

type ssoValidateResponse struct {
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	// Socket is the MariaDB unix socket path; non-empty means sso.php
	// must connect via socket (connect_type=socket in phpMyAdmin's
	// cfgupdate) rather than TCP. M25.1: once `skip-networking` lands
	// in my.cnf, Host/Port become advisory only.
	Socket string `json:"socket,omitempty"`
	OnlyDB string `json:"only_db,omitempty"`
	DB     string `json:"db,omitempty"`
}

// mariaDBSocketPath is the Debian/MariaDB default and what install.sh
// provisions. Duplicated from panel-api/internal/db/dsn.go so the
// validator response doesn't import a heavier package.
const mariaDBSocketPath = "/var/run/mysqld/mysqld.sock"

type ssoErrorResponse struct {
	Error string `json:"error"`
}

// validate handles POST /sso/phpmyadmin/validate.
// No JWT auth; Unix socket ACL is the boundary.
// Body: {"token":"<base64url>"}
// Returns 200 with credentials, 404 for unknown token, 410 for expired/used, 500 for errors.
func (h *ssoPhpMyAdminValidateHandler) validate(c *gin.Context) {
	ctx := c.Request.Context()

	var req ssoValidateRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ssoErrorResponse{Error: "invalid_request"})
		return
	}

	// Decode base64url token to raw bytes
	tokenBytes, err := base64.RawURLEncoding.DecodeString(req.Token)
	if err != nil {
		c.JSON(http.StatusBadRequest, ssoErrorResponse{Error: "invalid_token_encoding"})
		return
	}

	// Compute SHA-256 hash
	hash := sha256.Sum256(tokenBytes)
	hashStr := fmt.Sprintf("%x", hash[:])
	hashPrefix := hex.EncodeToString(hash[:4])

	// Consume token (atomic delete-and-return)
	token, err := h.cfg.Tokens.ConsumeByHash(ctx, hashStr)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Could be expired, already consumed (replay), or never existed
			outcome := "expired"
			h.auditLog(ctx, "", "", hashPrefix, outcome)
			c.JSON(http.StatusNotFound, ssoErrorResponse{Error: "not_found"})
			return
		}
		h.cfg.Log.ErrorContext(ctx, "consume token failed", "err", err)
		h.auditLog(ctx, "", "", hashPrefix, "unauthorized")
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	// M46 ADR-0099: admin all-DBs handoff. Per-user tokens ALWAYS
	// carry a real DatabaseID, so this early branch never affects
	// the per-user path below.
	if token.DatabaseID == ssoAdminAllSentinel {
		pw, rerr := os.ReadFile(pmaAdminPasswordFile)
		if rerr != nil {
			h.cfg.Log.ErrorContext(ctx, "pma admin secret read failed", "err", rerr)
			c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
			return
		}
		h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "validated:admin")
		c.JSON(http.StatusOK, ssoValidateResponse{
			User:     "jabali_pma_admin",
			Password: strings.TrimSpace(string(pw)),
			Host:     "localhost",
			Port:     3306,
			Socket:   mariaDBSocketPath,
			// OnlyDB/DB empty → phpMyAdmin shows ALL databases.
		})
		return
	}

	// Load user shadow credentials
	user, err := h.cfg.Users.FindByID(ctx, token.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "unauthorized")
			c.JSON(http.StatusNotFound, ssoErrorResponse{Error: "user_not_found"})
			return
		}
		h.cfg.Log.ErrorContext(ctx, "find user failed", "err", err)
		h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "unauthorized")
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	if user.MysqladminUsername == nil || user.MysqladminPasswordEnc == nil {
		h.cfg.Log.WarnContext(ctx, "user missing shadow credentials")
		h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "unauthorized")
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	// Decrypt password
	plaintextBytes, err := h.cfg.SSOKey.Open(user.MysqladminPasswordEnc)
	if err != nil {
		h.cfg.Log.ErrorContext(ctx, "decrypt password failed", "err", err)
		h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "unauthorized")
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	// Load database for the db name
	db, err := h.cfg.Databases.FindByID(ctx, token.DatabaseID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "unauthorized")
			c.JSON(http.StatusNotFound, ssoErrorResponse{Error: "database_not_found"})
			return
		}
		h.cfg.Log.ErrorContext(ctx, "find database failed", "err", err)
		h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "unauthorized")
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	// Log successful validation
	h.auditLog(ctx, token.UserID, token.DatabaseID, hashPrefix, "validated")

	resp := ssoValidateResponse{
		User:     *user.MysqladminUsername,
		Password: string(plaintextBytes),
		Host:     "localhost",
		Port:     3306,
		Socket:   mariaDBSocketPath,
		OnlyDB:   db.Name,
		DB:       db.Name,
	}

	c.JSON(http.StatusOK, resp)
}

// auditLog emits a structured slog line for SSO validation operations.
func (h *ssoPhpMyAdminValidateHandler) auditLog(ctx context.Context, userID, databaseID, tokenHashPrefix, outcome string) {
	h.cfg.Log.DebugContext(ctx, "sso_phpmyadmin_validate",
		"user_id", userID,
		"database_id", databaseID,
		"token_hash_prefix", tokenHashPrefix,
		"outcome", outcome,
	)
}
