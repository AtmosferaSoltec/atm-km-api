package security

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
)

var fast = Argon2Params{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestArgon2HashAndVerify(t *testing.T) {
	hasher := Argon2Hasher{Params: fast}
	hash, err := hasher.Hash("correct horse battery")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=1024,t=1,p=1$"))

	ok, err := hasher.Verify("correct horse battery", hash)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = hasher.Verify("wrong", hash)
	require.NoError(t, err)
	assert.False(t, ok)

	again, err := hasher.Hash("correct horse battery")
	require.NoError(t, err)
	assert.NotEqual(t, hash, again, "a fresh salt each time")
}

func TestArgon2VerifiesWithTheParametersInTheHash(t *testing.T) {
	old := Argon2Hasher{Params: fast}
	hash, err := old.Hash("password123")
	require.NoError(t, err)
	stronger := Argon2Hasher{Params: Argon2Params{MemoryKiB: 2048, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}}
	ok, err := stronger.Verify("password123", hash)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestArgon2RejectsMalformedHashes(t *testing.T) {
	hasher := Argon2Hasher{Params: fast}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=19$m=x$a$b"} {
		_, err := hasher.Verify("x", bad)
		assert.Error(t, err, bad)
	}
}

func TestJWTIssueAndVerify(t *testing.T) {
	tokens := JWTTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	claims := app.AccessClaims{UserID: uuid.New(), SessionID: uuid.New()}

	token, err := tokens.Issue(claims, now)
	require.NoError(t, err)

	got, err := tokens.Verify(token, now.Add(59*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, claims, got)

	_, err = tokens.Verify(token, now.Add(61*time.Minute))
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "expired after an hour")

	other := JWTTokens{Secret: []byte("another-secret-another-secret-xx"), TTL: time.Hour}
	_, err = other.Verify(token, now)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "wrong secret")
}

func TestJWTRejectsOtherAlgorithms(t *testing.T) {
	tokens := JWTTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	now := time.Now()
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": uuid.NewString(), "sid": uuid.NewString(), "exp": now.Add(time.Hour).Unix(),
	})
	token, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, err = tokens.Verify(token, now)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}
