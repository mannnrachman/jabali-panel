package kratosclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// VerifyBcryptPassthrough creates a disposable identity with a freshly-computed
// bcrypt hash, attempts an API-mode login with the corresponding plaintext,
// then deletes the identity. This validates end-to-end that Kratos is
// configured to accept our bcrypt format as stored credential material — the
// common failure mode is an otherwise-valid hash being rejected at login time
// because hashers.bcrypt.enabled is false or the cost is mismatched.
//
// Callers SHOULD invoke this once before any batch migration. On failure,
// ABORT the batch — do NOT fall back to per-user trials.
//
// The disposable identity is deleted whether the login succeeds or fails, so
// the canary never leaves Kratos with stale rows. Delete errors are logged
// into the returned error chain (not swallowed) so an orphan is still visible.
func (c *Client) VerifyBcryptPassthrough(ctx context.Context) error {
	suffix, err := randomCanarySuffix()
	if err != nil {
		return fmt.Errorf("canary: generate randomness: %w", err)
	}

	// Invalid TLD keeps the canary email from ever round-tripping through SMTP
	// if Kratos's verification flow is ever flipped on — `.invalid` is reserved
	// per RFC 2606.
	email := "canary-" + suffix + "@jabali.invalid"
	plaintext := "canary-pw-" + suffix

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), 12)
	if err != nil {
		return fmt.Errorf("canary: bcrypt hash: %w", err)
	}

	identityID, err := c.CreateIdentityWithPassword(ctx, AdminTraits{
		Email: email,
	}, string(hash))
	if err != nil {
		return fmt.Errorf("canary: create identity (kratos rejected the bcrypt hash format — check hashers.bcrypt.enabled in kratos.yml): %w", err)
	}

	loginErr := c.tryAPILogin(ctx, email, plaintext)

	delErr := c.DeleteIdentity(ctx, identityID)

	if loginErr != nil {
		return fmt.Errorf("canary: login failed (kratos stored the hash but could not verify it — bcrypt passthrough broken): %w", errors.Join(loginErr, delErr))
	}
	if delErr != nil {
		// Login worked — passthrough is fine — but we left a row. Warn the caller.
		return fmt.Errorf("canary: passthrough OK but identity cleanup failed: %w", delErr)
	}
	return nil
}

// tryAPILogin exercises Kratos's two-step API-mode login flow:
//
//  1. GET  {public}/self-service/login/api    → returns flow.id
//  2. POST {public}/self-service/login?flow=… → returns {session_token, session}
//
// Success means Kratos accepted the identifier+password pair and minted a
// session. We don't use the session for anything — it's discarded with the
// canary identity on the next call. API-mode is used instead of browser-mode
// because it avoids the cookie jar and CSRF dance; if the password verifier
// can authenticate the hash under API-mode, it will under browser-mode too
// (same credential backend).
func (c *Client) tryAPILogin(ctx context.Context, identifier, password string) error {
	// Step 1: initialise the flow.
	initReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.publicURL+"/self-service/login/api", nil)
	if err != nil {
		return fmt.Errorf("init login flow: %w", err)
	}
	initReq.Header.Set("Accept", "application/json")

	initResp, err := c.httpClient.Do(initReq)
	if err != nil {
		return fmt.Errorf("init login flow: %w", err)
	}
	defer initResp.Body.Close()

	if initResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(initResp.Body)
		return fmt.Errorf("init login flow: status %d: %s", initResp.StatusCode, string(body))
	}

	var flow struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(initResp.Body).Decode(&flow); err != nil {
		return fmt.Errorf("init login flow: decode: %w", err)
	}
	if flow.ID == "" {
		return fmt.Errorf("init login flow: empty flow id")
	}

	// Step 2: submit credentials.
	submitBody, err := json.Marshal(map[string]any{
		"method":     "password",
		"identifier": identifier,
		"password":   password,
	})
	if err != nil {
		return fmt.Errorf("submit login: marshal: %w", err)
	}

	submitReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.publicURL+"/self-service/login?flow="+flow.ID, bytes.NewReader(submitBody))
	if err != nil {
		return fmt.Errorf("submit login: request: %w", err)
	}
	submitReq.Header.Set("Content-Type", "application/json")
	submitReq.Header.Set("Accept", "application/json")

	submitResp, err := c.httpClient.Do(submitReq)
	if err != nil {
		return fmt.Errorf("submit login: do: %w", err)
	}
	defer submitResp.Body.Close()

	if submitResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(submitResp.Body)
		return fmt.Errorf("submit login: status %d: %s", submitResp.StatusCode, string(body))
	}

	var session struct {
		SessionToken string `json:"session_token"`
	}
	if err := json.NewDecoder(submitResp.Body).Decode(&session); err != nil {
		return fmt.Errorf("submit login: decode: %w", err)
	}
	if session.SessionToken == "" {
		return fmt.Errorf("submit login: empty session token")
	}
	return nil
}
