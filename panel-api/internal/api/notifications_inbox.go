package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// NotificationsInboxHandlerConfig wires the authenticated-user bell
// dropdown endpoints. History is required; Log is optional.
type NotificationsInboxHandlerConfig struct {
	History repository.NotificationHistoryRepository
	Log     *slog.Logger
}

// RegisterNotificationsInboxRoutes mounts:
//
//   - GET  /notifications/inbox?unread_only=&page=&page_size=  — current user + (admin-only) system-wide rows
//   - POST /notifications/inbox/:id/read                       — mark single row read
//   - POST /notifications/inbox/read-all                       — mark every unread row for current user read
//
// Routes require only an authenticated Kratos session (the parent group
// middleware enforces that). Regular users see their personal rows and
// no broadcast rows; admins also see user_id IS NULL (system-wide)
// entries so disk.full / service.down surface alongside personalised
// events without a second query.
func RegisterNotificationsInboxRoutes(g *gin.RouterGroup, cfg NotificationsInboxHandlerConfig) {
	if cfg.History == nil {
		panic("api.RegisterNotificationsInboxRoutes: cfg.History is nil")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	h := &inboxHandler{cfg: cfg}
	g.GET("/notifications/inbox", h.list)
	g.POST("/notifications/inbox/:id/read", h.markRead)
	g.POST("/notifications/inbox/read-all", h.readAll)
	g.DELETE("/notifications/inbox", h.clearAll)
}

type inboxHandler struct{ cfg NotificationsInboxHandlerConfig }

type inboxListResponse struct {
	Data       []models.NotificationHistory `json:"data"`
	Total      int                          `json:"total"`
	Page       int                          `json:"page"`
	PageSize   int                          `json:"page_size"`
	Unread     int64                        `json:"unread"`
	UnreadOnly bool                         `json:"unread_only"`
}

func (h *inboxHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	page, pageSize := parsePagination(c)
	opts := repository.ListOptions{Offset: (page - 1) * pageSize, Limit: pageSize}

	var rows []models.NotificationHistory
	var total int64
	var err error
	ctx := c.Request.Context()
	if claims.IsAdmin {
		rows, total, err = h.cfg.History.ListForAdminInbox(ctx, claims.UserID, opts)
	} else {
		rows, total, err = h.cfg.History.ListForUser(ctx, claims.UserID, opts)
	}
	if err != nil {
		h.cfg.Log.Error("inbox list failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list failed"})
		return
	}

	unreadOnly := c.Query("unread_only") == "true" || c.Query("unread_only") == "1"
	if unreadOnly {
		filtered := rows[:0]
		for _, r := range rows {
			if r.ReadAt == nil {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	var unread int64
	if claims.IsAdmin {
		unread, _ = h.cfg.History.CountUnreadForAdminInbox(ctx, claims.UserID)
	} else {
		unread, _ = h.cfg.History.CountUnreadForUser(ctx, claims.UserID)
	}

	c.JSON(http.StatusOK, inboxListResponse{
		Data:       rows,
		Total:      int(total),
		Page:       page,
		PageSize:   pageSize,
		Unread:     unread,
		UnreadOnly: unreadOnly,
	})
}

func (h *inboxHandler) markRead(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	id := c.Param("id")
	ctx := c.Request.Context()
	row, err := h.cfg.History.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load failed"})
		return
	}
	// Ownership: row must belong to caller, or be system-wide (NULL
	// user_id) and the caller must be an admin.
	ownsRow := row.UserID != nil && *row.UserID == claims.UserID
	adminSeesSystem := row.UserID == nil && claims.IsAdmin
	if !ownsRow && !adminSeesSystem {
		c.JSON(http.StatusForbidden, gin.H{"error": "not your notification"})
		return
	}
	if err := h.cfg.History.MarkRead(ctx, id); err != nil {
		h.cfg.Log.Error("mark read failed", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mark-read failed"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *inboxHandler) readAll(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	n, err := h.cfg.History.MarkAllReadForUser(c.Request.Context(), claims.UserID)
	if err != nil {
		h.cfg.Log.Error("mark all read failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mark-all failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": n})
}

func (h *inboxHandler) clearAll(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil || claims.UserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	n, err := h.cfg.History.DeleteAllForUser(c.Request.Context(), claims.UserID, claims.IsAdmin)
	if err != nil {
		h.cfg.Log.Error("clear all failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "clear failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}
