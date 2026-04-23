package api

import "github.com/gin-gonic/gin"

// RegisterM65Routes registers all M6.5 email feature routes.
// Called once from router.go; each sub-registration (forwarders, autoresponders, etc.)
// lives in its own Wave, preventing file collisions.
// Enables parallel Wave B/C development per ADR-0051.
func RegisterM65Routes(g *gin.RouterGroup) {
	registerForwarderRoutes(g)
	registerAutoresponderRoutes(g)
	registerCatchAllRoutes(g)
	registerDisclaimerRoutes(g)
	registerSharedFolderRoutes(g)
	registerMailLogRoutes(g)
}

// registerForwarderRoutes registers email forwarder (alias + external) endpoints.
// Implementation: Wave B (m65/email-forwarders).
func registerForwarderRoutes(g *gin.RouterGroup) {
	// TODO: Implement forwarder routes in Wave B.
}

// registerAutoresponderRoutes registers autoresponse/vacation endpoints.
// Implementation: Wave B (m65/email-autoresponders).
func registerAutoresponderRoutes(g *gin.RouterGroup) {
	// TODO: Implement autoresponder routes in Wave B.
}

// registerCatchAllRoutes registers domain-level catch-all endpoints.
// Implementation: Wave C (m65/domain-catchall).
func registerCatchAllRoutes(g *gin.RouterGroup) {
	// TODO: Implement catch-all routes in Wave C.
}

// registerDisclaimerRoutes registers domain-level disclaimer endpoints.
// Implementation: Wave C (m65/domain-disclaimer).
func registerDisclaimerRoutes(g *gin.RouterGroup) {
	// TODO: Implement disclaimer routes in Wave C.
}

// registerSharedFolderRoutes registers mailbox share (ACL) endpoints.
// Implementation: Wave C (m65/mailbox-shares).
func registerSharedFolderRoutes(g *gin.RouterGroup) {
	// TODO: Implement shared folder routes in Wave C.
}

// registerMailLogRoutes registers audit log endpoints.
// Implementation: Wave D (m65/mail-logs).
func registerMailLogRoutes(g *gin.RouterGroup) {
	// TODO: Implement mail log routes in Wave D.
}
