package middleware

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireLocalhost rejects every request whose remote address is not on
// the loopback interface. Used by panel-api endpoints the agent (or any
// other localhost daemon) calls — the SPA must never reach them. This
// is a defence-in-depth check on top of the network-level expectation
// that panel-api binds to 127.0.0.1.
//
// Implementation: split the host:port from RemoteAddr, parse, accept
// only when net.IP.IsLoopback() returns true. RemoteAddr can be empty
// in some test contexts; treat empty as non-loopback so tests must
// explicitly opt in by using a 127.0.0.1 client.
func RequireLocalhost() gin.HandlerFunc {
	return func(c *gin.Context) {
		remote := c.Request.RemoteAddr
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
