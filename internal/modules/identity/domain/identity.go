// Package domain holds the account rules: what a valid username and password
// are, and which errors authentication can produce.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Lengths. The password maximum keeps a huge password from burning CPU in argon2.
const (
	PasswordMinLength    = 8
	PasswordMaxLength    = 128
	DisplayNameMaxLength = 60
)

// Same rule as the users_username_format constraint: 3 to 40 characters,
// lowercase letters, digits, dot, dash or underscore. A DNI fits too.
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,39}$`)

// Field codes the clients know how to show.
const (
	CodeUsernameInvalid     = "USERNAME_INVALID"
	CodePasswordTooShort    = "PASSWORD_TOO_SHORT"
	CodePasswordTooLong     = "PASSWORD_TOO_LONG"
	CodeDisplayNameRequired = "DISPLAY_NAME_REQUIRED"
	CodeDisplayNameTooLong  = "DISPLAY_NAME_TOO_LONG"
)

// Authentication errors.
var (
	ErrUsernameTaken      = errors.New("username already taken")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionExpired     = errors.New("session expired")
	// ErrRefreshRace: the refresh token was rotated a moment ago by a parallel
	// request (two tabs, a double render). The client retries with the new one.
	ErrRefreshRace = errors.New("refresh token rotated moments ago")
	ErrNotFound    = errors.New("not found")
)

// TooManyAttemptsError: too many consecutive failures with one username.
type TooManyAttemptsError struct {
	RetryAfter time.Duration
}

func (e *TooManyAttemptsError) Error() string {
	return fmt.Sprintf("too many attempts, retry after %s", e.RetryAfter)
}

// FieldError is a field with a problem and the code clients know how to show.
type FieldError struct {
	Field string
	Code  string
}

// ValidationError gathers a form's problems in review order.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %v", e.Fields)
}

// Add records a problem.
func (e *ValidationError) Add(field, code string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Code: code})
}

// OrNil returns the error only if there are problems.
func (e *ValidationError) OrNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// User is an account.
type User struct {
	ID           uuid.UUID
	Username     string
	DisplayName  string
	PasswordHash string
	IsActive     bool
	CreatedAt    time.Time
}

// NormalizeUsername trims and lowercases: " Joel " is joel.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// ValidateUsername receives the already normalized username.
func ValidateUsername(username string, problems *ValidationError) {
	if !usernamePattern.MatchString(username) {
		problems.Add("username", CodeUsernameInvalid)
	}
}

// ValidateNewPassword is for a password being chosen. Only its length is
// enforced: composition rules push people to worse passwords.
func ValidateNewPassword(password string, problems *ValidationError) {
	switch length := utf8.RuneCountInString(password); {
	case length < PasswordMinLength:
		problems.Add("password", CodePasswordTooShort)
	case length > PasswordMaxLength:
		problems.Add("password", CodePasswordTooLong)
	}
}

// NormalizeDisplayName collapses inner spaces and trims.
func NormalizeDisplayName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// ValidateDisplayName receives the already normalized name.
func ValidateDisplayName(name string, problems *ValidationError) {
	switch {
	case name == "":
		problems.Add("display_name", CodeDisplayNameRequired)
	case utf8.RuneCountInString(name) > DisplayNameMaxLength:
		problems.Add("display_name", CodeDisplayNameTooLong)
	}
}
