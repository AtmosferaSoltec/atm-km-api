package security

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
)

// JWTTokens signs HS256 access tokens with JWT_ACCESS_SECRET.
type JWTTokens struct {
	Secret []byte
	TTL    time.Duration
}

type accessClaims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// Issue signs a token with sub (user), sid (session), iat and exp.
func (t JWTTokens) Issue(claims app.AccessClaims, now time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims{
		SessionID: claims.SessionID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   claims.UserID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.TTL)),
		},
	})
	signed, err := token.SignedString(t.Secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// Verify demands exactly HS256: it rejects "alg: none" and any other algorithm.
func (t JWTTokens) Verify(token string, now time.Time) (app.AccessClaims, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return t.Secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return app.AccessClaims{}, auth.ErrUnauthorized
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return app.AccessClaims{}, auth.ErrUnauthorized
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return app.AccessClaims{}, auth.ErrUnauthorized
	}
	return app.AccessClaims{UserID: userID, SessionID: sessionID}, nil
}
