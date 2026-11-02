package http

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/middleware"
)

// The web never keeps the refresh token where JavaScript can read it: it asks
// for it in an HttpOnly cookie with the X-Session-Transport header. A custom
// header forces the browser to preflight, so another site cannot forge the
// request; the cookie is also SameSite=Strict.
const (
	cookieTransport   = "cookie"
	RefreshCookieName = "km_refresh"
	// refreshCookiePath: the browser only sends it to the session routes.
	refreshCookiePath = "/api/v1/auth"
)

// CookieOptions configures the refresh token cookie.
type CookieOptions struct {
	// Secure is always on in the server; off in development (http://localhost).
	Secure bool
	MaxAge time.Duration
}

func usesCookie(c *gin.Context) bool {
	return c.GetHeader(middleware.SessionTransportHeader) == cookieTransport
}

func (h *Handler) setCookie(c *gin.Context, tokens app.Tokens) {
	ttl := tokens.RefreshExpiresIn
	if ttl <= 0 {
		ttl = h.cookie.MaxAge
	}
	h.writeCookie(c, tokens.RefreshToken, int(ttl.Seconds()))
}

func (h *Handler) clearCookie(c *gin.Context) {
	h.writeCookie(c, "", -1)
}

func (h *Handler) writeCookie(c *gin.Context, value string, maxAge int) {
	//nolint:gosec // Secure is on everywhere but local development over http
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}
