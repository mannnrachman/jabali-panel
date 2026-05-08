// Adminer SSO validate endpoint — POST /sso/adminer/validate.
//
// Mounted on the panel-api UDS (/run/jabali-panel/sso.sock); no JWT.
// Adminer's jabali-sso plugin POSTs `{"token": "..."}` and receives
// `{"driver":"server|pgsql","server":"...","username":"...","password":"...","db":"..."}`
// — driver tells Adminer which connect path to take, server is the
// connection target (UDS path for MariaDB, host:port for Postgres).
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

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
)

// pgLoopbackHostPort is what the Adminer pgsql driver dials. Postgres
// sockets live at /var/run/postgresql/.s.PGSQL.5432 — but Adminer's
// pgsql driver passes the value through to libpq's `host` parameter,
// which accepts a directory containing the socket. Setting this to
// /var/run/postgresql lets libpq find the right socket without us
// having to teach Adminer about MariaDB-style socket fields.
const pgLoopbackHost = "/var/run/postgresql"

type SSOAdminerValidateHandlerConfig struct {
	Databases repository.DatabaseRepository
	Users     repository.UserRepository
	Tokens    repository.AdminerSSOTokenRepository
	Adminer   *sso.AdminerService
	Log       *slog.Logger
}

func RegisterSSOAdminerValidateRoutes(g *gin.RouterGroup, cfg SSOAdminerValidateHandlerConfig) {
	h := &ssoAdminerValidateHandler{cfg: cfg}
	g.POST("/sso/adminer/validate", h.validate)
}

type ssoAdminerValidateHandler struct{ cfg SSOAdminerValidateHandlerConfig }

type ssoAdminerValidateRequest struct {
	Token string `json:"token" binding:"required"`
}

type ssoAdminerValidateResponse struct {
	Driver   string `json:"driver"`             // server (mariadb) | pgsql
	Server   string `json:"server"`             // socket dir or host:port
	Username string `json:"username"`
	Password string `json:"password"`
	DB       string `json:"db"`
}

func (h *ssoAdminerValidateHandler) validate(c *gin.Context) {
	ctx := c.Request.Context()

	var req ssoAdminerValidateRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ssoErrorResponse{Error: "invalid_request"})
		return
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(req.Token)
	if err != nil {
		c.JSON(http.StatusBadRequest, ssoErrorResponse{Error: "invalid_token_encoding"})
		return
	}
	hash := sha256.Sum256(tokenBytes)
	hashStr := fmt.Sprintf("%x", hash[:])
	hashPrefix := hex.EncodeToString(hash[:4])

	token, err := h.cfg.Tokens.ConsumeByHash(ctx, hashStr)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.audit(ctx, "", "", "", hashPrefix, "expired_or_unknown")
			c.JSON(http.StatusNotFound, ssoErrorResponse{Error: "not_found"})
			return
		}
		h.cfg.Log.ErrorContext(ctx, "consume adminer token failed", "err", err)
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	user, err := h.cfg.Users.FindByID(ctx, token.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "user_not_found")
			c.JSON(http.StatusNotFound, ssoErrorResponse{Error: "user_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	db, err := h.cfg.Databases.FindByID(ctx, token.DatabaseID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "db_not_found")
			c.JSON(http.StatusNotFound, ssoErrorResponse{Error: "database_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	password, err := h.cfg.Adminer.DecryptShadowPassword(user, token.Engine)
	if err != nil {
		h.cfg.Log.WarnContext(ctx, "decrypt shadow failed", "err", err, "engine", token.Engine)
		h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "decrypt_fail")
		c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
		return
	}

	resp := ssoAdminerValidateResponse{
		DB:       db.Name,
		Password: password,
	}
	switch token.Engine {
	case "mariadb":
		resp.Driver = "server"
		// Adminer's MySQLi backend reads $server like
		// `host[:port]` and only treats a colon-prefixed value
		// as a Unix socket path. Bare `/var/run/mysqld/mysqld.sock`
		// is interpreted as a hostname → DNS lookup → connect
		// failure (the symptom: Adminer login form submits but
		// the page returns to the form, looking "stuck").
		resp.Server = "localhost:" + mariaDBSocketPath
		if user.MysqladminUsername == nil {
			h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "shadow_username_nil")
			c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
			return
		}
		resp.Username = *user.MysqladminUsername
	case "postgres":
		resp.Driver = "pgsql"
		resp.Server = pgLoopbackHost
		if user.PgadminUsername == nil {
			h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "shadow_username_nil")
			c.JSON(http.StatusInternalServerError, ssoErrorResponse{Error: "internal"})
			return
		}
		resp.Username = *user.PgadminUsername
	default:
		h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "unknown_engine")
		c.JSON(http.StatusBadRequest, ssoErrorResponse{Error: "unknown_engine"})
		return
	}

	h.audit(ctx, token.UserID, token.DatabaseID, token.Engine, hashPrefix, "validated")
	c.JSON(http.StatusOK, resp)
}

func (h *ssoAdminerValidateHandler) audit(ctx context.Context, userID, databaseID, engine, hashPrefix, outcome string) {
	h.cfg.Log.DebugContext(ctx, "sso_adminer_validate",
		"user_id", userID,
		"database_id", databaseID,
		"engine", engine,
		"token_hash_prefix", hashPrefix,
		"outcome", outcome,
	)
}
