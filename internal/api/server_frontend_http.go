package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Only Gin's NoRoute path reaches this handler: built-in and configured routes,
// including dynamic routes, retain precedence without mutating Gin on reload.
func (s *Server) pluginFrontendHTTPNoRoute(c *gin.Context) {
	if s.pluginHost == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	// The host pins the selected route before choosing authentication. Scoped
	// bearer routes must never invoke the global access manager, even on failure.
	coreAuth := func() (string, bool) {
		// Bound reads by configured global authentication providers as before.
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, pluginapi.FrontendHTTPMaxBodyBytes)
		}
		AuthMiddleware(s.accessManager)(c)
		if c.IsAborted() {
			return "", false
		}
		// Plugin resources must never share an anonymous namespace when core auth
		// is unconfigured or returns no principal.
		callerScope := session.CallerScope(c.GetString("userApiKey"))
		if callerScope == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return "", false
		}
		return callerScope, true
	}
	if s.pluginHost.ServeFrontendHTTP(c.Writer, c.Request, coreAuth) {
		c.Abort()
		return
	}
	c.AbortWithStatus(http.StatusNotFound)
}
