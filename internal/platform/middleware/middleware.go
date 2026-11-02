// Package middleware holds what runs on every request: request id, panic
// recovery, access log, CORS, per-IP rate limit and body size.
package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// RequestIDHeader travels in the request and in the response.
const RequestIDHeader = "X-Request-Id"

// SessionTransportHeader lets the web ask for its refresh token in a cookie.
const SessionTransportHeader = "X-Session-Transport"

// Only a client id that cannot pollute the logs is honored.
var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestID takes the client's X-Request-Id if it is safe, or generates one, and
// leaves in the context a logger that carries it on every line.
func RequestID(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !safeRequestID.MatchString(id) {
			id = ids.New().String()
		}
		c.Header(RequestIDHeader, id)
		ctx := logger.WithContext(c.Request.Context(), base.With("request_id", id))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// Recover turns a panic into a 500 with the error shape. A broken handler
// neither kills the process nor leaves other requests unanswered.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.From(c.Request.Context()).Error("panic recovered",
					"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				apierror.Respond(c, apierror.ErrInternal)
			}
		}()
		c.Next()
	}
}

// AccessLog logs method, route, status and duration. Never headers or bodies:
// they carry tokens and passwords. Health checks are skipped: Docker calls them
// every 30 seconds.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		path := c.FullPath()
		if strings.HasSuffix(path, "/health") || strings.HasSuffix(path, "/health/ready") {
			return
		}
		if path == "" {
			path = "unmatched"
		}
		logger.From(c.Request.Context()).Info("request",
			"method", c.Request.Method,
			"route", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

// CORS only lets the configured origins through. It only matters to browsers.
func CORS(allowed []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && slices.Contains(allowed, origin) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			// The web sends its session cookie.
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Expose-Headers", RequestIDHeader+", Retry-After")
			if c.Request.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+SessionTransportHeader+", "+RequestIDHeader)
				h.Set("Access-Control-Max-Age", "600")
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

// BodyLimit cuts bodies larger than limit; reading them gives the handler an
// *http.MaxBytesError that apierror turns into a 413. larger gives specific
// routes (photo uploads) their own limit, by Gin route.
func BodyLimit(limit int64, larger map[string]int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := limit
		if routeLimit, ok := larger[c.FullPath()]; ok {
			limit = routeLimit
		}
		if c.Request.ContentLength > limit {
			apierror.Respond(c, apierror.ErrPayloadTooLarge)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// RateLimiter limits requests per IP in memory. One replica is enough; with
// several, the counter would have to be shared.
type RateLimiter struct {
	perMinute int
	mu        sync.Mutex
	visitors  map[string]*visitor
	now       func() time.Time
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter allows perMinute requests per minute per IP, with bursts of
// the same size.
func NewRateLimiter(perMinute int) *RateLimiter {
	return &RateLimiter{perMinute: perMinute, visitors: map[string]*visitor{}, now: time.Now}
}

// Middleware returns the Gin handler. The IP is c.ClientIP(), which only trusts
// X-Forwarded-For from a trusted proxy (nginx).
func (l *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		limiter := l.limiterFor(c.ClientIP())
		reservation := limiter.Reserve()
		if delay := reservation.Delay(); delay > 0 {
			reservation.Cancel()
			c.Header("Retry-After", strconv.Itoa(int(delay.Seconds())+1))
			apierror.Respond(c, apierror.ErrTooManyRequests)
			return
		}
		c.Next()
	}
}

func (l *RateLimiter) limiterFor(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Lazy cleanup: whoever has not shown up for 5 minutes has a full quota anyway.
	if len(l.visitors) > 10_000 {
		for key, v := range l.visitors {
			if now.Sub(v.lastSeen) > 5*time.Minute {
				delete(l.visitors, key)
			}
		}
	}
	v, ok := l.visitors[ip]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.perMinute)), l.perMinute)}
		l.visitors[ip] = v
	}
	v.lastSeen = now
	return v.limiter
}
