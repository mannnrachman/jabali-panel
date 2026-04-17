package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ReconcileService interface {
	ReconcileAll(ctx context.Context) error
	ReconcileAllForce(ctx context.Context) error
	ReconcilePHPPools(ctx context.Context)
}

type ReconcileHandlerConfig struct {
	Reconciler ReconcileService
	Log        *slog.Logger
}

type reconcileHandler struct {
	cfg *ReconcileHandlerConfig
}

type reconcileRequest struct {
	Scope string `json:"scope" binding:"required,oneof=all"`
	Force bool   `json:"force"`
}

type reconcileResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func RegisterReconcileRoutes(g *gin.RouterGroup, cfg *ReconcileHandlerConfig) {
	h := &reconcileHandler{cfg: cfg}
	g.POST("/reconcile", h.run)
	g.POST("/reconcile/php-pools", h.reconcilePHPPoolsHandler)
}

func (h *reconcileHandler) run(c *gin.Context) {
	var req reconcileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, reconcileResponse{
			Status:  "error",
			Message: fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	h.cfg.Log.Info("reconcile request",
		"scope", req.Scope,
		"force", req.Force,
		"requester", c.GetString("user_id"))

	var err error
	if req.Force {
		err = h.cfg.Reconciler.ReconcileAllForce(c.Request.Context())
	} else {
		err = h.cfg.Reconciler.ReconcileAll(c.Request.Context())
	}

	if err != nil {
		h.cfg.Log.Error("reconcile failed", "err", err)
		c.JSON(http.StatusInternalServerError, reconcileResponse{
			Status:  "error",
			Message: fmt.Sprintf("reconciliation failed: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, reconcileResponse{
		Status:  "success",
		Message: "reconciliation complete",
	})
}

func (h *reconcileHandler) reconcilePHPPoolsHandler(c *gin.Context) {
	h.cfg.Log.Info("php pools reconciliation request",
		"requester", c.GetString("user_id"))

	h.cfg.Reconciler.ReconcilePHPPools(c.Request.Context())

	c.JSON(http.StatusOK, reconcileResponse{
		Status:  "success",
		Message: "php pools reconciliation initiated",
	})
}
