package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// UserHandlerConfig plugs the users resource handlers into the router. Repo
// is the only required field; BcryptCost defaults to bcrypt.DefaultCost;
// Agent is optional and used for best-effort OS user provisioning.
type UserHandlerConfig struct {
	Repo            repository.UserRepository
	BcryptCost      int
	Agent           agent.AgentInterface
	StrictRateLimit gin.HandlerFunc
	Domains         repository.DomainRepository
	Packages        repository.PackageRepository
	Reconciler      *reconciler.Reconciler
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	CookieName      string
	CookieSecure    bool
	Log             *slog.Logger

	// M20: Kratos hook. When AuthProvider == "kratos" AND KratosClient is
	// non-nil, POST /users atomically creates both a panel user row and a
	// Kratos identity. When "legacy" or KratosClient is nil, the Kratos call
	// is skipped — existing flow is preserved so the flag can flip cleanly.
	KratosClient *kratosclient.Client
	AuthProvider string
}

// kratosEnabled reports whether the current config selects the Kratos provider
// AND the client is wired. Both checks are required because the config may be
// set to "kratos" before the client is constructed (e.g. in tests).
func (c UserHandlerConfig) kratosEnabled() bool {
	return c.AuthProvider == "kratos" && c.KratosClient != nil
}

// Paging defaults/limits chosen so a misbehaving client can't issue
// million-row sweeps, and the SPA can ask for reasonable page sizes without
// extra config.
const (
	defaultUsersPageSize = 20
	maxUsersPageSize     = 200
)

// RegisterUserRoutes mounts /users* on g. g must already enforce RequireAuth.
//
// Authorisation:
//   - list / create / delete → admin only (RequireAdmin)
//   - get / patch            → admin or owner (RequireOwner)
//
// Fine-grained rules (can't demote the last admin, owner must provide
// current_password to change their own password, etc.) live inside the
// handler functions where they can return informative errors.
func RegisterUserRoutes(g *gin.RouterGroup, cfg UserHandlerConfig) {
	if cfg.BcryptCost == 0 {
		cfg.BcryptCost = bcrypt.DefaultCost
	}
	h := &userHandler{cfg: cfg}

	g.GET("/users", middleware.RequireAdmin(), h.list)
	g.POST("/users", middleware.RequireAdmin(), h.create)
	g.GET("/users/:id", middleware.RequireOwner("id"), h.get)
	g.PATCH("/users/:id", middleware.RequireOwner("id"), h.update)
	g.DELETE("/users/:id", middleware.RequireAdmin(), h.delete)
	reprov := []gin.HandlerFunc{middleware.RequireAdmin()}
	if cfg.StrictRateLimit != nil {
		reprov = append(reprov, cfg.StrictRateLimit)
	}
	reprov = append(reprov, h.reprovision)
	g.POST("/users/:id/reprovision", reprov...)

	// Admin-only per-user systemd slice status (Step 8 of per-user-slices).
	g.GET("/admin/users/:id/slice-status", middleware.RequireAdmin(), h.sliceStatus)
}

type userHandler struct{ cfg UserHandlerConfig }

// ---------- request / response shapes ----------

type createUserRequest struct {
	Email           string  `json:"email"                    binding:"required,email"`
	Password        string  `json:"password"                 binding:"required,min=10"`
	Username        *string `json:"username,omitempty"       binding:"omitempty,min=1,max=32"`
	NameFirst       string  `json:"name_first"`
	NameLast        string  `json:"name_last"`
	IsAdmin         bool    `json:"is_admin"`
	PackageID       *string `json:"package_id,omitempty"`
	SkipProvision   bool    `json:"skip_provision,omitempty"`
}

// updateUserRequest uses pointers so the handler can distinguish "omit this
// field" from "set this field to the zero value" (e.g. clearing a name).
type updateUserRequest struct {
	Email           *string `json:"email,omitempty"            binding:"omitempty,email"`
	NameFirst       *string `json:"name_first,omitempty"`
	NameLast        *string `json:"name_last,omitempty"`
	Password        *string `json:"password,omitempty"         binding:"omitempty,min=10"`
	CurrentPassword *string `json:"current_password,omitempty"`
	IsAdmin         *bool   `json:"is_admin,omitempty"`
	PackageID       *string `json:"package_id,omitempty"`
}

type reprovisionRequest struct {
	Password string `json:"password" binding:"required,min=10"`
}

type listUsersResponse struct {
	Data     []models.User `json:"data"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// ---------- handlers ----------

func (h *userHandler) list(c *gin.Context) {
	page, pageSize, opts := parseListOptions(c, defaultUsersPageSize, maxUsersPageSize)
	// Optional ?is_admin=true|false scopes the result. Anything else is
	// silently ignored so the legacy "list all" behaviour stays intact.
	switch c.Query("is_admin") {
	case "true":
		t := true
		opts.IsAdmin = &t
	case "false":
		f := false
		opts.IsAdmin = &f
	}
	users, total, err := h.cfg.Repo.List(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, listUsersResponse{
		Data:     users,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

func (h *userHandler) get(c *gin.Context) {
	u, err := h.cfg.Repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.translateErr(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

func (h *userHandler) create(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), h.cfg.BcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Compute effective username: use req.Username if provided, else derive from email prefix.
	// For admins, username stays NULL. For regular users, validate and set.
	var effectiveUsername *string
	if !req.IsAdmin {
		if req.Username != nil {
			effectiveUsername = req.Username
		} else {
			derived := linuxUserFromEmail(req.Email)
			effectiveUsername = &derived
		}
		// Validate the username against POSIX regex.
		if effectiveUsername != nil && !validUsername(*effectiveUsername) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "invalid_username",
				"detail": "username must match regex ^[a-z_][a-z0-9_-]{0,31}$",
			})
			return
		}
	}

	// Validate package_id if provided.
	if req.PackageID != nil && *req.PackageID != "" {
		if h.cfg.Packages == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		_, err := h.cfg.Packages.FindByID(c.Request.Context(), *req.PackageID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":  "invalid_package_id",
					"detail": "hosting package not found",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}

	u := &models.User{
		ID:           ids.NewULID(),
		Email:        req.Email,
		Username:     effectiveUsername,
		NameFirst:    req.NameFirst,
		NameLast:     req.NameLast,
		PasswordHash: string(hash),
		IsAdmin:      req.IsAdmin,
		PackageID:    req.PackageID,
	}
	if err := h.cfg.Repo.Create(c.Request.Context(), u); err != nil {
		// Check if the error is a username collision specifically.
		if isConflict(err) && effectiveUsername != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "username_taken"})
			return
		}
		h.translateErr(c, err)
		return
	}

	// M20: atomic Kratos identity creation.
	// Runs only when the feature flag selects Kratos. On failure we undo the
	// panel DB row (compensating delete) so the two systems can't drift. The
	// failure surface is explicitly 5xx — callers retry. We never return 201
	// for a half-created user.
	if h.cfg.kratosEnabled() {
		traits := kratosclient.AdminTraits{
			Email:   u.Email,
			IsAdmin: u.IsAdmin,
		}
		if u.Username != nil {
			traits.Username = *u.Username
		}

		identityID, err := h.cfg.KratosClient.CreateIdentityWithPassword(c.Request.Context(), traits, u.PasswordHash)
		if err != nil {
			// Roll back the panel row so retries don't hit a username/email conflict.
			if delErr := h.cfg.Repo.Delete(c.Request.Context(), u.ID); delErr != nil {
				slog.Error("kratos create failed and panel rollback also failed — orphan row",
					"user_id", u.ID, "email", u.Email, "kratos_err", err, "rollback_err", delErr)
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": "identity_provider_failed", "detail": err.Error()})
			return
		}

		// LinkKratosIdentity writes only that one column; Repo.Update's
		// allowlist excludes kratos_identity_id so it would silently drop
		// the write.
		u.KratosIdentityID = &identityID
		if err := h.cfg.Repo.LinkKratosIdentity(c.Request.Context(), u.ID, identityID); err != nil {
			// Undo both sides: delete the Kratos identity so re-create is safe,
			// then delete the panel row. Best-effort — if either unwind call
			// fails, log it so the operator sees the orphan.
			if delErr := h.cfg.KratosClient.DeleteIdentity(c.Request.Context(), identityID); delErr != nil {
				slog.Error("panel link failed and kratos rollback also failed — orphan identity",
					"user_id", u.ID, "identity_id", identityID, "link_err", err, "rollback_err", delErr)
			}
			if delErr := h.cfg.Repo.Delete(c.Request.Context(), u.ID); delErr != nil {
				slog.Error("panel link failed and panel rollback also failed — orphan row",
					"user_id", u.ID, "link_err", err, "rollback_err", delErr)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}

	// Best-effort OS user provisioning. Write DB first, then agent call.
	// If agent fails, return 201 with provision_warning but keep the DB row.
	if h.cfg.Agent != nil && !req.SkipProvision && !req.IsAdmin && effectiveUsername != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		_, err := h.cfg.Agent.Call(ctx, "user.create", map[string]any{
			"username": *effectiveUsername,
			"home_dir": "/home/" + *effectiveUsername,
			"shell":    "/bin/bash",
			"password": req.Password,
		})
		if err != nil {
			slog.Warn("user agent provisioning failed",
				"user_id", u.ID, "email", u.Email, "err", err)
			c.JSON(http.StatusCreated, struct {
				*models.User
				ProvisionWarning string `json:"provision_warning"`
			}{
				User:             u,
				ProvisionWarning: "user saved but OS account provisioning failed: " + err.Error(),
			})
			return
		}
	}

	c.JSON(http.StatusCreated, u)
}

func (h *userHandler) update(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		// Defence in depth — RequireAuth + RequireOwner should have stopped this.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Only admins may toggle is_admin. A non-admin owner who sends the field
	// (even with their own current value) gets 403 — easier to reason about
	// than silently stripping it.
	if req.IsAdmin != nil && !claims.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	ctx := c.Request.Context()
	id := c.Param("id")

	existing, err := h.cfg.Repo.FindByID(ctx, id)
	if err != nil {
		h.translateErr(c, err)
		return
	}

	// Owner changing their own password must re-authenticate with the
	// current one. Admins bypass this — that's the definition of admin.
	if req.Password != nil && !claims.IsAdmin {
		if req.CurrentPassword == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "current_password_required"})
			return
		}
		if err := bcrypt.CompareHashAndPassword(
			[]byte(existing.PasswordHash), []byte(*req.CurrentPassword),
		); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
			return
		}
	}

	// Refuse demoting the last admin — otherwise a careless PATCH locks
	// everyone out. Check BEFORE mutating anything.
	if req.IsAdmin != nil && existing.IsAdmin && !*req.IsAdmin {
		n, err := h.cfg.Repo.CountAdmins(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		if n <= 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "cannot_demote_last_admin"})
			return
		}
	}

	// Apply field-level updates. Repo.Update explicitly excludes is_admin.
	if req.Email != nil {
		existing.Email = *req.Email
	}
	if req.NameFirst != nil {
		existing.NameFirst = *req.NameFirst
	}
	if req.NameLast != nil {
		existing.NameLast = *req.NameLast
	}
	if req.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), h.cfg.BcryptCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		existing.PasswordHash = string(hash)
	}

	// Validate and apply package_id if provided (including clearing it with empty string).
	if req.PackageID != nil {
		if *req.PackageID != "" {
			if h.cfg.Packages == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			_, err := h.cfg.Packages.FindByID(ctx, *req.PackageID)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					c.JSON(http.StatusBadRequest, gin.H{
						"error":  "invalid_package_id",
						"detail": "hosting package not found",
					})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
				return
			}
			existing.PackageID = req.PackageID
		} else {
			// Empty string means clear the package assignment
			existing.PackageID = nil
		}
	}

	if err := h.cfg.Repo.Update(ctx, existing); err != nil {
		h.translateErr(c, err)
		return
	}

	if req.Password != nil && claims.UserID != id {
		slog.Info("audit",
			"event", "admin_password_reset",
			"actor_id", claims.UserID,
			"target_id", id,
			"target_email", existing.Email)
	}

	// Best-effort password sync to OS user. Only for non-admins
	// (admins have full system access). Run in background so client
	// sees DB update immediately; if agent fails, user can still log in
	// with the new password via SSH.
	if req.Password != nil && !claims.IsAdmin && h.cfg.Agent != nil && existing.Username != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := h.cfg.Agent.Call(ctx, "user.password", map[string]any{
				"username": *existing.Username,
				"password": *req.Password,
			})
			if err != nil {
				slog.Warn("user password sync to OS failed",
					"user_id", id, "email", existing.Email, "err", err)
			}
		}()
	}

	// Flip is_admin in its own call so the repo's privilege-safe Update
	// doesn't have to widen. Admin-only guard was checked above.
	if req.IsAdmin != nil {
		if err := h.cfg.Repo.SetAdmin(ctx, id, *req.IsAdmin); err != nil {
			h.translateErr(c, err)
			return
		}
		existing.IsAdmin = *req.IsAdmin
	}

	c.JSON(http.StatusOK, existing)
}

func (h *userHandler) delete(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	id := c.Param("id")

	// Self-delete lockout protection: the only way out would be through the
	// DB, which is worse than just refusing here.
	if id == claims.UserID {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot_delete_self"})
		return
	}

	// Last-admin lockout protection, same reasoning as demotion.
	target, err := h.cfg.Repo.FindByID(c.Request.Context(), id)
	if err != nil {
		h.translateErr(c, err)
		return
	}
	if target.IsAdmin {
		n, err := h.cfg.Repo.CountAdmins(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		if n <= 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "cannot_delete_last_admin"})
			return
		}
	}

	// Cascade-delete all domains owned by this user. DB first, then
	// out-of-band agent teardown via the reconciler. Best-effort: any
	// per-domain failure is logged, never fails the user delete.
	if h.cfg.Domains != nil {
		// Page through to avoid loading millions of rows in one shot.
		// Realistically a user has a handful of domains, but bound the
		// loop anyway.
		const batchSize = 500
		for {
			owned, _, err := h.cfg.Domains.ListByUserID(c.Request.Context(), id, repository.ListOptions{Limit: batchSize})
			if err != nil {
				slog.Warn("cascade delete: list user domains failed",
					"user_id", id, "err", err)
				break
			}
			if len(owned) == 0 {
				break
			}
			for i := range owned {
				d := &owned[i]
				name := d.Name
				if err := h.cfg.Domains.Delete(c.Request.Context(), d.ID); err != nil {
					slog.Warn("cascade delete: domain DB delete failed",
						"user_id", id, "domain_id", d.ID, "domain", name, "err", err)
					continue
				}
				if h.cfg.Reconciler != nil {
					// Fire-and-forget — don't block the user delete on nginx
					// teardown. Use a fresh context because c.Request.Context
					// ends when the handler returns.
					name := name // capture
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						h.cfg.Reconciler.ReconcileDeleted(ctx, name)
					}()
				}
			}
			if len(owned) < batchSize {
				break
			}
		}
	}

	// Capture username BEFORE deleting so we can tear down the OS user
	// even after the DB row is gone. For admins, username is NULL.
	var username string
	if target.Username != nil {
		username = *target.Username
	}

	if err := h.cfg.Repo.Delete(c.Request.Context(), id); err != nil {
		h.translateErr(c, err)
		return
	}

	// Best-effort OS teardown. remove_home defaults to false so tenant data is
	// preserved for manual recovery; pass ?purge=true to delete home directory.
	purge := c.Query("purge") == "true"
	if h.cfg.Agent != nil && username != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := h.cfg.Agent.Call(ctx, "user.delete", map[string]any{
				"username":    username,
				"remove_home": purge,
			})
			if err != nil {
				slog.Warn("user agent teardown failed",
					"user_id", id, "username", username, "err", err)
			}
		}()
	}

	if purge {
		slog.Info("audit",
			"event", "user_purge_deleted",
			"actor_id", claims.UserID,
			"target_id", id,
			"target_email", target.Email)
	}

	c.Status(http.StatusNoContent)
}

func (h *userHandler) reprovision(c *gin.Context) {
	var req reprovisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()
	id := c.Param("id")

	user, err := h.cfg.Repo.FindByID(ctx, id)
	if err != nil {
		h.translateErr(c, err)
		return
	}

	// Admins are panel-only; reprovisioning them would create a stray
	// OS account that shouldn't exist.
	if user.IsAdmin {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin_has_no_os_account"})
		return
	}

	// Username should always be set for non-admin users.
	if user.Username == nil || *user.Username == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot_derive_username"})
		return
	}
	username := *user.Username

	// This endpoint is deliberately agent-first + synchronous. Manual
	// recovery needs to tell the admin whether the OS side actually
	// converged — firing a goroutine and returning 200 would hide the
	// real failure. If the agent call fails, the DB is untouched.
	if h.cfg.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_unavailable"})
		return
	}
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, agentErr := h.cfg.Agent.Call(agentCtx, "user.create", map[string]any{
		"username": username,
		"home_dir": "/home/" + username,
		"shell":    "/bin/bash",
		"password": req.Password,
	})
	if agentErr != nil {
		// If the OS user already exists, steer the admin to the
		// password-sync path instead — useradd would just fail.
		var ae *agent.AgentError
		if errors.As(agentErr, &ae) && ae.Code == agent.CodeAlreadyExists {
			c.JSON(http.StatusConflict, gin.H{
				"error":  "os_user_exists",
				"detail": "OS user already exists — use PATCH /users/:id { password } to sync the password only",
			})
			return
		}
		slog.Warn("reprovision agent call failed",
			"user_id", id, "username", username, "err", agentErr)
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  "agent_error",
			"detail": agentErr.Error(),
		})
		return
	}

	// Agent succeeded — update DB hash so the panel password matches.
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), h.cfg.BcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	user.PasswordHash = string(hash)
	if err := h.cfg.Repo.Update(ctx, user); err != nil {
		h.translateErr(c, err)
		return
	}

	claims := ginctx.Claims(c)
	if claims != nil {
		slog.Info("audit",
			"event", "user_reprovisioned",
			"actor_id", claims.UserID,
			"target_id", id,
			"target_email", user.Email)
	}

	c.JSON(http.StatusOK, user)
}

// ---------- helpers ----------

// translateErr maps repository sentinels to HTTP responses. Keep the branches
// narrow — any unknown error is internal.
func (h *userHandler) translateErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	case errors.Is(err, repository.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "conflict"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
	}
}

// parsePagination reads ?page=&page_size= with sane defaults. Negative or
// out-of-range values are clamped rather than rejected — the SPA can send
// whatever and still get data.
func parsePagination(c *gin.Context) (page, pageSize int) {
	page, _ = strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(c.Query("page_size"))
	if pageSize < 1 {
		pageSize = defaultUsersPageSize
	}
	if pageSize > maxUsersPageSize {
		pageSize = maxUsersPageSize
	}
	return page, pageSize
}

var usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// validUsername returns true if s matches the POSIX username regex:
// ^[a-z_][a-z0-9_-]{0,31}$
func validUsername(s string) bool {
	return usernameRe.MatchString(s)
}

// linuxUserFromEmail derives a Linux username from an email. Takes the
// part before '@'. Callers are expected to validate downstream (the
// agent's user.create enforces the POSIX regex).
func linuxUserFromEmail(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return ""
}
