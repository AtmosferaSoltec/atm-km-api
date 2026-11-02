// Package http translates HTTP to identity use cases and back. No business
// rules: parse, call the service, respond.
package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
)

// Service is the use cases the handler needs.
type Service interface {
	Login(ctx context.Context, in app.LoginInput) (app.AuthResult, error)
	Refresh(ctx context.Context, refreshToken string) (app.Tokens, error)
	Logout(ctx context.Context, refreshToken string) error
	Me(ctx context.Context, userID uuid.UUID) (domain.User, error)
}

// The module's own codes (error codes, not credentials).
//
//nolint:gosec // G101 false positive
const (
	codeInvalidCredentials  = "INVALID_CREDENTIALS"
	codeSessionExpired      = "SESSION_EXPIRED"
	codeRefreshTokenRotated = "REFRESH_TOKEN_ROTATED"
)

var (
	errInvalidCredentials = apierror.New(http.StatusUnauthorized, codeInvalidCredentials, "Usuario o contraseña incorrectos.")
	errSessionExpired     = apierror.New(http.StatusUnauthorized, codeSessionExpired, "Tu sesión terminó. Inicia sesión de nuevo.")
	errRefreshRace        = apierror.New(http.StatusConflict, codeRefreshTokenRotated, "La sesión se acaba de renovar. Inténtalo de nuevo.")
)

// Handler serves the authentication routes.
type Handler struct {
	service Service
	cookie  CookieOptions
}

// NewHandler creates the handler.
func NewHandler(service Service, cookie CookieOptions) *Handler {
	return &Handler{service: service, cookie: cookie}
}

// Routes mounts the routes. limited are public routes under the narrow
// LOGIN_RATE_LIMIT_PER_MINUTE; protected demand a token. Refresh stays under the
// general limit: its token is 256 random bits, nothing to guess, and every app
// open or reload uses it.
func (h *Handler) Routes(public, limited, protected gin.IRoutes) {
	limited.POST("/auth/login", h.login)
	public.POST("/auth/refresh", h.refresh)
	public.POST("/auth/logout", h.logout)
	protected.GET("/me", h.me)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// RefreshToken is empty (and omitted) when it travels in the web's cookie.
type tokensResponse struct {
	AccessToken          string `json:"access_token"`
	AccessTokenExpiresIn int    `json:"access_token_expires_in"`
	RefreshToken         string `json:"refresh_token,omitempty"`
}

type authResponse struct {
	User   userResponse   `json:"user"`
	Tokens tokensResponse `json:"tokens"`
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	result, err := h.service.Login(c.Request.Context(), app.LoginInput{
		Username: req.Username, Password: req.Password, UserAgent: c.Request.UserAgent(), Web: usesCookie(c),
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if usesCookie(c) {
		h.setCookie(c, result.Tokens)
		result.Tokens.RefreshToken = ""
	}
	apierror.OK(c, authResponse{User: toUser(result.User), Tokens: toTokens(result.Tokens)})
}

func (h *Handler) refresh(c *gin.Context) {
	token, ok := h.refreshTokenOf(c)
	if !ok {
		return
	}
	tokens, err := h.service.Refresh(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, domain.ErrSessionExpired) && usesCookie(c) {
			h.clearCookie(c)
		}
		respondError(c, err)
		return
	}
	if usesCookie(c) {
		h.setCookie(c, tokens)
		tokens.RefreshToken = ""
	}
	apierror.OK(c, gin.H{"tokens": toTokens(tokens)})
}

// logout always answers 204: logging out must work even with a dead token.
func (h *Handler) logout(c *gin.Context) {
	token, ok := h.refreshTokenOf(c)
	if !ok {
		return
	}
	if usesCookie(c) {
		h.clearCookie(c)
	}
	if token != "" {
		if err := h.service.Logout(c.Request.Context(), token); err != nil {
			respondError(c, err)
			return
		}
	}
	apierror.NoContent(c)
}

// refreshTokenOf reads the refresh token: from the cookie if the request comes
// from the web, from the body otherwise.
func (h *Handler) refreshTokenOf(c *gin.Context) (string, bool) {
	if usesCookie(c) {
		token, _ := c.Cookie(RefreshCookieName)
		return token, true
	}
	var req refreshRequest
	if !apierror.BindJSON(c, &req) {
		return "", false
	}
	return req.RefreshToken, true
}

func (h *Handler) me(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	user, err := h.service.Me(c.Request.Context(), userID)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.OK(c, toUser(user))
}

func respondError(c *gin.Context, err error) {
	var tooMany *domain.TooManyAttemptsError
	switch {
	case errors.As(err, &tooMany):
		c.Header("Retry-After", strconv.Itoa(int(tooMany.RetryAfter.Seconds())+1))
		apierror.Respond(c, apierror.ErrTooManyRequests)
	case errors.Is(err, domain.ErrInvalidCredentials):
		apierror.Respond(c, errInvalidCredentials)
	case errors.Is(err, domain.ErrSessionExpired):
		apierror.Respond(c, errSessionExpired)
	case errors.Is(err, domain.ErrRefreshRace):
		apierror.Respond(c, errRefreshRace)
	case errors.Is(err, domain.ErrNotFound):
		apierror.Respond(c, apierror.ErrUnauthorized)
	default:
		apierror.Respond(c, err)
	}
}

func toUser(user domain.User) userResponse {
	return userResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, CreatedAt: user.CreatedAt.UTC()}
}

func toTokens(tokens app.Tokens) tokensResponse {
	return tokensResponse{
		AccessToken:          tokens.AccessToken,
		AccessTokenExpiresIn: int(tokens.ExpiresIn.Seconds()),
		RefreshToken:         tokens.RefreshToken,
	}
}
