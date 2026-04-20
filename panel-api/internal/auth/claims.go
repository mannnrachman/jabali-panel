package auth

// AccessClaims is the resolved identity for an authenticated request. It is
// populated by middleware (RequireKratosSession) from the Kratos whoami
// response + the panel users row, and stashed on the gin.Context via ginctx.
// Downstream handlers read it to answer "who is calling?" and enforce
// ownership/admin checks.
//
// The struct predates M20 — it used to embed jwt.RegisteredClaims because
// we minted our own JWTs. After M20 removed the legacy JWT surface the only
// fields anyone actually reads are UserID, Email, IsAdmin, so those are the
// only fields that live here now.
type AccessClaims struct {
	UserID  string
	Email   string
	IsAdmin bool
}
