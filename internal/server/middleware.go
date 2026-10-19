package server

import (
	"cfui/internal/logger"
	"net/http"
	"runtime/debug"
	"strings"
)

// PanicRecoveryMiddleware recovers from panics in HTTP handlers
func PanicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log the panic with stack trace
				logger.Sugar.Errorf("HTTP handler panic: %v", err)
				logger.Sugar.Errorf("Stack trace:\n%s", debug.Stack())

				// Return 500 Internal Server Error to client
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs all HTTP requests. High-frequency polling endpoints
// are logged at debug level so they don't flood the log file (and the UI's
// live log panel, which would otherwise echo its own polling forever).
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && isPollingPath(r.URL.Path) {
			logger.Sugar.Debugf("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		} else {
			logger.Sugar.Infof("%s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		}
		next.ServeHTTP(w, r)
	})
}

func isPollingPath(path string) bool {
	switch path {
	case "/api/status", "/api/tunnels", "/api/ddns/status", "/api/logs/recent", "/api/s3/files/sync":
		return true
	}
	return strings.HasPrefix(path, "/api/tunnels/") && strings.HasSuffix(path, "/status")
}

// ChainMiddleware chains multiple middleware together
func ChainMiddleware(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
