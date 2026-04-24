package middleware

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireLocalhost rejects every request whose remote address is not on
// the loopback interface OR delivered over the local unix socket. Used
// by panel-api endpoints the agent (or any other local daemon) calls —
// the SPA must never reach them. This is a defence-in-depth check on
// top of the bind-level expectation that panel-api binds to 127.0.0.1
// (legacy) or /run/jabali-panel/api.sock (M25).
//
// Implementation notes:
//   - For TCP peers, split host:port from RemoteAddr and accept only
//     when net.IP.IsLoopback() returns true.
//   - For unix-socket peers, Go's net/http sets RemoteAddr to "@" (the
//     empty abstract address) since unix sockets don't have a remote
//     IP. A unix-socket connection is localhost by definition — the
//     peer process has to be on the same host (or in a mount namespace
//     that bind-mounted the socket, which is equivalent). Accept these.
//   - Empty RemoteAddr is also treated as unix-socket for safety: some
//     adapters drop the "@" sentinel. The net effect is the same since
//     any non-unix peer through an HTTP server will have a RemoteAddr.
func RequireLocalhost() gin.HandlerFunc {
	return func(c *gin.Context) {
		remote := c.Request.RemoteAddr
		// Unix-socket accept: RemoteAddr is "@" or "" (see net/http
		// httputil docs). Accept — the connection is by definition
		// local to this host.
		if remote == "" || remote == "@" {
			c.Next()
			return
		}
		host, _, err := net.SplitHostPort(remote)
		if err != nil {
			// Fallback: some adapters supply a bare host with no port.
			host = remote
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "localhost_only"})
			return
		}
		c.Next()
	}
}
