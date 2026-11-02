// Package auth carries who is making the request in its context. The user_id
// only ever comes from here (from the token): no module accepts it from the
// body or the URL.
package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
)

// Principal is the authenticated user and the session (device) the request comes from.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

// ErrUnauthorized: token missing, invalid, expired, or from a revoked session.
var ErrUnauthorized = errors.New("unauthorized")

// Authenticator validates an access token. The identity module implements it.
type Authenticator interface {
	Authenticate(ctx context.Context, accessToken string) (Principal, error)
}

type contextKey struct{}

// WithPrincipal stores who is making the request.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}

// FromContext returns who is making the request. It only exists on protected routes.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	return p, ok
}

// UserID returns the authenticated user. Handlers behind Require can rely on it;
// if it is missing anyway, the request is rejected instead of running unscoped.
func UserID(c *gin.Context) (uuid.UUID, bool) {
	p, ok := FromContext(c.Request.Context())
	if !ok {
		apierror.Respond(c, apierror.ErrUnauthorized)
		return uuid.Nil, false
	}
	return p.UserID, true
}

// Require demands "Authorization: Bearer <access token>". Everything mounted
// behind it is private: forgetting something leaves a route protected, not open.
func Require(authenticator Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			apierror.Respond(c, apierror.ErrUnauthorized)
			return
		}
		principal, err := authenticator.Authenticate(c.Request.Context(), token)
		if errors.Is(err, ErrUnauthorized) {
			apierror.Respond(c, apierror.ErrUnauthorized)
			return
		}
		if err != nil {
			apierror.Respond(c, err)
			return
		}
		c.Request = c.Request.WithContext(WithPrincipal(c.Request.Context(), principal))
		c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}
