package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPMiddleware returns a Gin middleware that records HTTP metrics.
func HTTPMiddleware(m *Metrics) gin.HandlerFunc {
	if m == nil {
		m = DefaultMetrics
	}

	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		c.Next()

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		m.RecordHTTPRequest(c.Request.Method, path, status, duration)
	}
}

// NormalizePath normalizes URL paths for metrics to avoid cardinality explosion.
func NormalizePath(path string) string {
	// Common patterns to normalize
	// This prevents high cardinality from dynamic path parameters
	switch {
	case len(path) > 50:
		return path[:50] + "..."
	default:
		return path
	}
}
