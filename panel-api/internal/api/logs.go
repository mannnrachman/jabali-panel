package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type LogHandlerConfig struct {
	LogAccessStreams repository.LogAccessStreamRepository
	Domains          repository.DomainRepository
	Users            repository.UserRepository
}

type logHandler struct{ cfg LogHandlerConfig }

type createLogAccessRequest struct {
	DomainID string `json:"domain_id,omitempty"`
	LogType  string `json:"log_type" binding:"required,oneof=access error goaccess"`
}

type logAccessResponse struct {
	StreamKey string    `json:"stream_key"`
	ExpiresAt time.Time `json:"expires_at"`
	WebsocketURL string `json:"websocket_url"`
}

// RegisterLogRoutes sets up log-related API endpoints
func RegisterLogRoutes(g *gin.RouterGroup, cfg LogHandlerConfig) {
	h := &logHandler{cfg: cfg}
	logs := g.Group("/logs")
	logs.POST("/access", h.createAccess)
	logs.DELETE("/access/:stream_key", h.deleteAccess)
	logs.GET("/types", h.listTypes)
	logs.GET("/stream/:stream_key", h.streamAccess)
}

// listTypes returns available log types and their descriptions
func (h *logHandler) listTypes(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	types := []gin.H{
		{
			"type":        "access",
			"name":        "Access Logs",
			"description": "Nginx access logs showing HTTP requests",
			"realtime":    true,
		},
		{
			"type":        "error",
			"name":        "Error Logs",
			"description": "Nginx error logs showing server errors",
			"realtime":    true,
		},
		{
			"type":        "goaccess",
			"name":        "GoAccess Report",
			"description": "Real-time web log analyzer dashboard",
			"realtime":    true,
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"data": types,
	})
}

// createAccess creates a time-limited access stream for log viewing
func (h *logHandler) createAccess(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req createLogAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Validate domain access if domain_id is provided
	if req.DomainID != "" {
		if h.cfg.Domains == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "domain service not available"})
			return
		}

		domain, err := h.cfg.Domains.FindByID(c.Request.Context(), req.DomainID)
		if err != nil {
			if err == repository.ErrNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			}
			return
		}

		// Non-admin users can only access their own domain logs
		if !claims.IsAdmin && domain.UserID != claims.UserID {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	} else if !claims.IsAdmin {
		// Non-admin users must specify a domain
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain_id required for non-admin users"})
		return
	}

	// Check rate limit - max 5 concurrent streams per user
	if h.cfg.LogAccessStreams != nil {
		count, err := h.cfg.LogAccessStreams.CountByUserID(c.Request.Context(), claims.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		if count >= 5 {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many active log streams"})
			return
		}
	}

	// Generate cryptographically secure stream key
	keyBytes := make([]byte, 16) // 32 hex chars
	if _, err := rand.Read(keyBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	streamKey := hex.EncodeToString(keyBytes)

	// Create stream record with 15-minute expiry
	expiresAt := time.Now().Add(15 * time.Minute)
	var domainID *string
	if req.DomainID != "" {
		domainID = &req.DomainID
	}
	stream := &models.LogAccessStream{
		ID:        ids.NewULID(),
		UserID:    claims.UserID,
		DomainID:  domainID,
		LogType:   req.LogType,
		StreamKey: streamKey,
		ExpiresAt: expiresAt,
	}

	if err := h.cfg.LogAccessStreams.Create(c.Request.Context(), stream); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Build WebSocket URL
	scheme := "ws"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/api/v1/logs/stream/%s", scheme, c.Request.Host, streamKey)

	c.JSON(http.StatusCreated, logAccessResponse{
		StreamKey:    streamKey,
		ExpiresAt:    expiresAt,
		WebsocketURL: wsURL,
	})
}

// deleteAccess revokes a log access stream
func (h *logHandler) deleteAccess(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	streamKey := c.Param("stream_key")
	if streamKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stream_key required"})
		return
	}

	// Validate stream ownership
	stream, err := h.cfg.LogAccessStreams.FindByStreamKey(c.Request.Context(), streamKey)
	if err != nil {
		if err == repository.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		}
		return
	}

	// Users can only delete their own streams, admins can delete any
	if !claims.IsAdmin && stream.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	if err := h.cfg.LogAccessStreams.DeleteByID(c.Request.Context(), stream.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.Status(http.StatusNoContent)
}

// validateLogType ensures the log type is supported
func validateLogType(logType string) error {
	switch logType {
	case "access", "error", "goaccess":
		return nil
	default:
		return fmt.Errorf("unsupported log type: %s", logType)
	}
}

// validateStreamKey validates stream key format for security
func validateStreamKey(key string) error {
	if len(key) != 32 {
		return fmt.Errorf("invalid stream key length")
	}

	// Must be hex-encoded
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("invalid stream key format")
	}

	return nil
}

// logFilePathForDomain returns the log file path for a domain and log type.
// Domain name passes through a strict allowlist (alnum, dot, hyphen) so the
// resulting filepath.Join can never escape /var/log/nginx/. The output is
// re-asserted with filepath.Clean + a HasPrefix check at the call site (see
// resolveLogPath) so the path-validation invariant holds even if a future
// caller forgets it.
func logFilePathForDomain(domainName, logType string) (string, error) {
	if !isSafeDomainSegment(domainName) {
		return "", fmt.Errorf("invalid domain name")
	}
	baseDir := "/var/log/nginx"
	switch logType {
	case "access":
		return filepath.Join(baseDir, fmt.Sprintf("%s-access.log", domainName)), nil
	case "error":
		return filepath.Join(baseDir, fmt.Sprintf("%s-error.log", domainName)), nil
	default:
		return "", fmt.Errorf("unsupported log type for file path: %s", logType)
	}
}

// isSafeDomainSegment is the path-safety predicate the WS streamer uses
// before joining the operator-supplied domain into the log path. Permits
// only RFC-1035-shape labels + dot separators; rejects path metacharacters
// outright (no '/', no '\', no '..', no '%', no spaces).
func isSafeDomainSegment(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return !strings.Contains(s, "..")
}

// resolveLogPath turns a stream's (LogType, DomainID) into an absolute file
// path under /var/log/nginx/. Returns an error for unsupported log types or
// any path that doesn't end up rooted under the nginx log directory after
// Clean — defence-in-depth against a future code path that forgets to
// validate domain names.
func (h *logHandler) resolveLogPath(ctx context.Context, stream *models.LogAccessStream) (string, error) {
	const baseDir = "/var/log/nginx"
	var raw string
	if stream.DomainID != nil {
		if h.cfg.Domains == nil {
			return "", fmt.Errorf("domain service not available")
		}
		domain, err := h.cfg.Domains.FindByID(ctx, *stream.DomainID)
		if err != nil {
			return "", fmt.Errorf("find domain: %w", err)
		}
		raw, err = logFilePathForDomain(domain.Name, stream.LogType)
		if err != nil {
			return "", err
		}
	} else {
		switch stream.LogType {
		case "access":
			raw = filepath.Join(baseDir, "access.log")
		case "error":
			raw = filepath.Join(baseDir, "error.log")
		default:
			return "", fmt.Errorf("unsupported global log type: %s", stream.LogType)
		}
	}
	clean := filepath.Clean(raw)
	if !strings.HasPrefix(clean, baseDir+"/") {
		return "", fmt.Errorf("path escapes log root: %s", clean)
	}
	return clean, nil
}

// streamAccess upgrades the request to a WebSocket and tails the log file
// resolved from the stream record. Auth is the stream-key fallback (panel-
// api hands out time-limited keys via POST /logs/access; only those keys
// validate here). The Origin check pins the WS connection to the same Host
// the request hit, so a victim's session cookie can't be paired with an
// attacker-controlled WS URL via cross-origin hijack.
//
// Lifecycle: the spawned `tail -f` lives in its own process group so a
// single Kill on the negative pgid cleans up children too. The handler
// returns when EITHER the WS read loop sees a control frame (Close, ping
// timeout) OR the scanner exits (file rotated, host shutdown). Both
// outcomes signal the context cancel; the deferred kill + Wait drains the
// subprocess.
//
// Wire shape (server → client, JSON text frames):
//
//	{"timestamp":"<RFC3339>","line":"<raw>","type":"<access|error>"}
func (h *logHandler) streamAccess(c *gin.Context) {
	streamKey := c.Param("stream_key")
	if err := validateStreamKey(streamKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream key"})
		return
	}
	stream, err := h.cfg.LogAccessStreams.FindByStreamKey(c.Request.Context(), streamKey)
	if err != nil {
		if err == repository.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		}
		return
	}
	if time.Now().After(stream.ExpiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "stream expired"})
		return
	}

	logPath, err := h.resolveLogPath(c.Request.Context(), stream)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid log target", "detail": err.Error()})
		return
	}

	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   1024,
		WriteBufferSize:  4096,
		// Same-origin gate. Browsers send Origin on WS upgrade; non-
		// browser clients (curl, the CLI smoke test) usually omit it
		// — we accept those because the stream-key + 15-min expiry
		// is the real auth. The Origin check defends specifically
		// against cookie-credential cross-origin abuse.
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			u, perr := url.Parse(origin)
			if perr != nil {
				return false
			}
			return strings.EqualFold(u.Host, r.Host)
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrader already wrote the HTTP error.
		return
	}
	defer conn.Close()

	h.streamLogTail(conn, stream.LogType, logPath)
}

// streamLogTail does the actual `tail -f` → WS pump. Extracted so unit
// tests can mock the upgrader path independently.
func (h *logHandler) streamLogTail(conn *websocket.Conn, logType, logPath string) {
	const (
		writeWait    = 5 * time.Second
		pongWait     = 60 * time.Second
		pingPeriod   = 30 * time.Second
		maxLineBytes = 64 * 1024  // matches default bufio.Scanner cap
		maxRateBytes = 5 * 1024 * 1024 // 5 MiB/s ceiling per stream
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "tail", "-F", "-n", "10", logPath)
	// Own process group so a Kill on -pgid sweeps children too. Without
	// this, a panicking tail child can outlive the context cancel.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"error":"failed to start log streaming"}`))
		return
	}
	if err := cmd.Start(); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"error":"failed to start log streaming"}`))
		return
	}
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		_ = cmd.Wait()
	}()

	// WS keepalive — pong handler resets read deadline; a missed pong
	// trips the ReadMessage in the reader goroutine and cancels ctx.
	conn.SetReadLimit(512)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Reader goroutine: turns Close / ping-timeout / network blip into
	// a context cancel.
	go func() {
		defer cancel()
		for {
			if _, _, rerr := conn.ReadMessage(); rerr != nil {
				return
			}
		}
	}()

	// Pinger goroutine: keeps the WS alive across NAT idle timeouts.
	go func() {
		t := time.NewTicker(pingPeriod)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 8*1024), maxLineBytes)
	rateStart := time.Now()
	rateBytes := 0
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		// Per-second rate cap. Crude but bounded — protects the panel
		// from a sudden access-log torrent (gzip-bomb test, scraper
		// burst) that would otherwise pin the WS goroutine.
		if elapsed := time.Since(rateStart); elapsed >= time.Second {
			rateStart = time.Now()
			rateBytes = 0
		}
		rateBytes += len(line)
		if rateBytes > maxRateBytes {
			continue
		}
		frame, _ := json.Marshal(map[string]any{
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"line":      line,
			"type":      logType,
		})
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			return
		}
	}
}

