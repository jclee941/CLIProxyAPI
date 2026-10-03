package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// inFlightMiddleware counts the API requests being answered, which a restart would cut.
// Management calls are left out because a deploy asks through them while it waits, and
// websocket sessions because they stay open until their client leaves.
func (s *Server) inFlightMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/v0/management/") || path == "/v0/management" || c.IsWebsocket() {
			c.Next()
			return
		}
		s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		c.Next()
	}
}

func (s *Server) getInFlight(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"in_flight": s.inFlight.Load()})
}
