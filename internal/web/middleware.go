package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ctxKey string

const userCtxKey ctxKey = "user_id"

// publicRoutes are paths that don't require authentication.
var publicRoutes = map[string]bool{
	"/":                   true,
	"/styles.css":         true,
	"/app.js":             true,
	"/api/health":         true,
	"/api/auth/login":     true,
	"/api/auth/register":  true,
}

// userIDFromCtx extracts the authenticated user ID from the request context.
func userIDFromCtx(ctx context.Context) (int, error) {
	v, ok := ctx.Value(userCtxKey).(int)
	if !ok {
		return 0, fmt.Errorf("no user in context")
	}
	return v, nil
}

// extractToken pulls the Bearer token from the Authorization header.
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// SecurityHeaders sets OWASP-recommended security response headers (A05).
func (s *Server) SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware protects all non-public routes with JWT validation (A01).
func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicRoutes[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
			return
		}
		userID, err := ParseToken(token, s.store.Secret())
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid or expired session"))
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// --- rate limiter (A07: brute-force protection) ---

type rateLimiter struct {
	mu     sync.Mutex
	counts map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{counts: map[string][]time.Time{}}
}

// Allow returns true if the key is under the limit for the time window.
func (rl *rateLimiter) Allow(key string, max int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	var recent []time.Time
	for _, t := range rl.counts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= max {
		rl.counts[key] = recent
		return false
	}
	recent = append(recent, now)
	rl.counts[key] = recent
	return true
}

func clientIP(r *http.Request) string {
	if ff := r.Header.Get("X-Forwarded-For"); ff != "" {
		return strings.Split(ff, ",")[0]
	}
	return r.RemoteAddr
}
