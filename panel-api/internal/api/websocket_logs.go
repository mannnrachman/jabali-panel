package api

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow connections from same origin for now
		// TODO: Implement proper origin validation
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type LogStreamHandlerConfig struct {
	LogAccessStreams repository.LogAccessStreamRepository
	Domains          repository.DomainRepository
	Log              *slog.Logger
}

// RegisterLogStreamRoutes sets up WebSocket log streaming endpoints
func RegisterLogStreamRoutes(r gin.IRouter, cfg LogStreamHandlerConfig) {
	handler := &logStreamHandler{cfg: cfg}
	r.GET("/logs/stream/:stream_key", handler.streamLogs)
}

type logStreamHandler struct{ cfg LogStreamHandlerConfig }

// streamLogs handles WebSocket connections for real-time log streaming
func (h *logStreamHandler) streamLogs(c *gin.Context) {
	streamKey := c.Param("stream_key")
	if streamKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stream_key required"})
		return
	}

	// Validate stream key format
	if err := validateStreamKey(streamKey); err != nil {
		h.cfg.Log.Warn("invalid stream key format", "key", streamKey, "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stream key"})
		return
	}

	// Look up and validate the stream
	stream, err := h.cfg.LogAccessStreams.FindByStreamKey(c.Request.Context(), streamKey)
	if err != nil {
		if err == repository.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "stream not found or expired"})
		} else {
			h.cfg.Log.Error("failed to find stream", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		}
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.cfg.Log.Error("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	// Set connection timeouts
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	// Handle different log types
	switch stream.LogType {
	case "access", "error":
		h.streamLogFile(conn, stream)
	case "goaccess":
		h.streamGoAccess(conn, stream)
	default:
		h.cfg.Log.Error("unsupported log type", "type", stream.LogType)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "unsupported log type"))
		return
	}
}

// streamLogFile streams nginx access or error logs
func (h *logStreamHandler) streamLogFile(conn *websocket.Conn, stream *models.LogAccessStream) {
	// Determine log file path
	var logPath string
	var err error

	if stream.DomainID != nil {
		// Get domain for per-domain logs
		domain, err := h.cfg.Domains.FindByID(context.Background(), *stream.DomainID)
		if err != nil {
			h.cfg.Log.Error("failed to find domain", "domain_id", *stream.DomainID, "err", err)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "domain not found"))
			return
		}
		logPath, err = logFilePathForDomain(domain.Name, stream.LogType)
	} else {
		// System-wide logs (admin only)
		logPath, err = systemLogPath(stream.LogType)
	}

	if err != nil {
		h.cfg.Log.Error("failed to determine log path", "err", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "invalid log path"))
		return
	}

	// Verify log file exists and is readable
	if _, err := os.Stat(logPath); err != nil {
		h.cfg.Log.Warn("log file not accessible", "path", logPath, "err", err)
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Log file not found: %s\n", filepath.Base(logPath))))
		return
	}

	// Start tailing the log file
	cmd := exec.Command("tail", "-f", "-n", "50", logPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.cfg.Log.Error("failed to create stdout pipe", "err", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "failed to start log stream"))
		return
	}

	if err := cmd.Start(); err != nil {
		h.cfg.Log.Error("failed to start tail command", "err", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "failed to start log stream"))
		return
	}

	// Ensure we clean up the process
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()

	// Stream log lines to WebSocket
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		// Check if stream has expired
		if time.Now().After(stream.ExpiresAt) {
			h.cfg.Log.Info("stream expired", "stream_key", stream.StreamKey)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stream expired"))
			return
		}

		// Send log line to client
		line := scanner.Text()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(line+"\n")); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.cfg.Log.Error("websocket write failed", "err", err)
			}
			return
		}

		// Reset write deadline for next message
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}

	if err := scanner.Err(); err != nil {
		h.cfg.Log.Error("log scanner error", "err", err)
	}
}

// streamGoAccess streams GoAccess real-time HTML reports
func (h *logStreamHandler) streamGoAccess(conn *websocket.Conn, stream *models.LogAccessStream) {
	// For GoAccess, we need to generate and serve the real-time HTML report
	// This is a more complex implementation that requires running GoAccess with --real-time-html

	// Determine access log path for GoAccess input
	var accessLogPath string
	var err error

	if stream.DomainID != nil {
		domain, err := h.cfg.Domains.FindByID(context.Background(), *stream.DomainID)
		if err != nil {
			h.cfg.Log.Error("failed to find domain for goaccess", "domain_id", *stream.DomainID, "err", err)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "domain not found"))
			return
		}
		accessLogPath, err = logFilePathForDomain(domain.Name, "access")
	} else {
		accessLogPath, err = systemLogPath("access")
	}

	if err != nil {
		h.cfg.Log.Error("failed to determine access log path for goaccess", "err", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "invalid log path"))
		return
	}

	// Verify access log file exists. goaccess fails on missing input;
	// surface a clear message via the iframe instead of crash-looping.
	if _, statErr := os.Stat(accessLogPath); statErr != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(
			"<html><body style='font-family:sans-serif;padding:2em;background:#1f1f1f;color:#fff'>"+
				"<h2>No access log yet</h2>"+
				"<p>File <code>%s</code> doesn't exist. "+
				"GoAccess will start once nginx writes traffic to this domain.</p>"+
				"</body></html>", filepath.Base(accessLogPath))))
		return
	}

	// Generate a static GoAccess HTML report from the current log on a
	// 10s cadence and push the full document over the WS. The frontend
	// rewrites the iframe contents on every "<html…" message arrival
	// (see LogStreamModal.tsx:onmessage). This is intentionally NOT
	// real-time WS — running goaccess --real-time-html requires a
	// second WS port + reverse proxy hop and isn't worth the complexity
	// for the panel use case.
	render := func() error {
		// --no-progress: don't write progress bar to stdout (would
		// corrupt the HTML stream). --no-html-last-updated:
		// deterministic output so iframe doesn't churn diff-only
		// changes. Use a 5s parse timeout via SIGTERM if goaccess
		// hangs on a malformed line.
		cmdCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, "goaccess",
			accessLogPath,
			"--log-format=COMBINED",
			"-o", "/dev/stdout",
			"--no-progress",
			"--no-html-last-updated")
		out, err := cmd.Output()
		if err != nil {
			h.cfg.Log.Warn("goaccess render failed", "err", err, "path", accessLogPath)
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, out)
	}

	// Initial render — without this the UI stays on "Waiting…" until
	// the first 10s tick fires.
	if err := render(); err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(
			"<html><body style='font-family:sans-serif;padding:2em;background:#1f1f1f;color:#fff'>"+
				"<h2>GoAccess error</h2><pre>%s</pre></body></html>", err.Error())))
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(stream.ExpiresAt) {
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stream expired"))
				return
			}
			if err := render(); err != nil {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		}
	}
}

// systemLogPath returns the path to system-wide nginx logs
func systemLogPath(logType string) (string, error) {
	switch logType {
	case "access":
		return "/var/log/nginx/access.log", nil
	case "error":
		return "/var/log/nginx/error.log", nil
	default:
		return "", fmt.Errorf("unsupported system log type: %s", logType)
	}
}