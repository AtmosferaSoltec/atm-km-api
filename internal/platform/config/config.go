// Package config reads and validates the environment at startup. If a required
// variable is missing or malformed the app does not start: an incomplete .env
// shows up during the deploy, not while serving someone.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment is the value of APP_ENV.
type Environment string

// Supported environments.
const (
	Development Environment = "development"
	Production  Environment = "production"
	Test        Environment = "test"
)

// minSecretLength: openssl rand -base64 48 yields 64 characters.
const minSecretLength = 32

// Defaults for the optional variables.
const (
	defaultTimezone           = "America/Lima"
	defaultPhotosDir          = "data/photos"
	defaultPhotoRetentionDays = 90
)

// Config is the validated environment.
type Config struct {
	Env                     Environment
	Port                    string
	DatabaseURL             string
	JWTAccessSecret         string
	AccessTokenTTL          time.Duration
	RefreshTokenTTL         time.Duration
	CORSOrigins             []string
	RateLimitPerMinute      int
	LoginRateLimitPerMinute int
	// Location is where "today" is computed: the owner lives in Peru.
	Location *time.Location
	Photos   PhotosConfig
}

// PhotosConfig is where odometer photos live on disk and for how long.
type PhotosConfig struct {
	Dir       string
	Retention time.Duration
}

// IsProduction tells whether it runs on the server.
func (c Config) IsProduction() bool { return c.Env == Production }

// Load reads the process environment.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads the variables through lookup, so it can be tested without
// touching the environment.
func LoadFrom(lookup func(string) string) (Config, error) {
	r := reader{lookup: lookup}

	cfg := Config{
		Env:                     r.environment("APP_ENV"),
		Port:                    r.port("PORT"),
		DatabaseURL:             r.databaseURL("DATABASE_URL"),
		JWTAccessSecret:         r.secret("JWT_ACCESS_SECRET"),
		AccessTokenTTL:          time.Duration(r.positiveInt("ACCESS_TOKEN_TTL_SECONDS")) * time.Second,
		RefreshTokenTTL:         time.Duration(r.positiveInt("REFRESH_TOKEN_TTL_DAYS")) * 24 * time.Hour,
		CORSOrigins:             r.list("CORS_ORIGIN"),
		RateLimitPerMinute:      r.positiveInt("RATE_LIMIT_PER_MINUTE"),
		LoginRateLimitPerMinute: r.positiveInt("LOGIN_RATE_LIMIT_PER_MINUTE"),
		Location:                r.location("APP_TIMEZONE"),
		Photos: PhotosConfig{
			Dir:       r.optionalOr("PHOTOS_DIR", defaultPhotosDir),
			Retention: time.Duration(r.optionalDays("PHOTO_RETENTION_DAYS", defaultPhotoRetentionDays)) * 24 * time.Hour,
		},
	}

	if cfg.IsProduction() {
		r.requireInProduction("CORS_ORIGIN", len(cfg.CORSOrigins) > 0)
	}

	if len(r.problems) > 0 {
		return Config{}, fmt.Errorf("invalid environment:\n  - %s", strings.Join(r.problems, "\n  - "))
	}
	return cfg, nil
}

// DatabaseURL is all the migrate subcommand needs.
func DatabaseURL() (string, error) {
	r := reader{lookup: os.Getenv}
	value := r.databaseURL("DATABASE_URL")
	if len(r.problems) > 0 {
		return "", errors.New(r.problems[0])
	}
	return value, nil
}

type reader struct {
	lookup   func(string) string
	problems []string
}

func (r *reader) fail(format string, args ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, args...))
}

func (r *reader) required(name string) string {
	value := strings.TrimSpace(r.lookup(name))
	if value == "" {
		r.fail("%s is required", name)
	}
	return value
}

func (r *reader) optional(name string) string {
	return strings.TrimSpace(r.lookup(name))
}

func (r *reader) optionalOr(name, fallback string) string {
	if value := r.optional(name); value != "" {
		return value
	}
	return fallback
}

func (r *reader) environment(name string) Environment {
	value := Environment(r.required(name))
	switch value {
	case Development, Production, Test, "":
	default:
		r.fail("%s must be development, production or test (got %q)", name, value)
	}
	return value
}

func (r *reader) port(name string) string {
	value := r.required(name)
	if value == "" {
		return value
	}
	if n, err := strconv.Atoi(value); err != nil || n < 1 || n > 65535 {
		r.fail("%s must be a port number (got %q)", name, value)
	}
	return value
}

func (r *reader) databaseURL(name string) string {
	value := r.required(name)
	if value == "" {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		// The value is never printed: it carries the password.
		r.fail("%s must be a postgresql:// URL", name)
	}
	return value
}

func (r *reader) secret(name string) string {
	value := r.required(name)
	if value != "" && len(value) < minSecretLength {
		r.fail("%s must have at least %d characters (generate it with: openssl rand -base64 48)", name, minSecretLength)
	}
	return value
}

func (r *reader) positiveInt(name string) int {
	value := r.required(name)
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		r.fail("%s must be a positive whole number (got %q)", name, value)
	}
	return n
}

func (r *reader) list(name string) []string {
	var items []string
	for _, item := range strings.Split(r.optional(name), ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func (r *reader) location(name string) *time.Location {
	value := r.optionalOr(name, defaultTimezone)
	loc, err := time.LoadLocation(value)
	if err != nil {
		r.fail("%s must be an IANA time zone such as America/Lima (got %q)", name, value)
		return time.UTC
	}
	return loc
}

func (r *reader) requireInProduction(name string, present bool) {
	if !present {
		r.fail("%s is required in production", name)
	}
}

func (r *reader) optionalDays(name string, fallback int) int {
	value := r.optional(name)
	if value == "" {
		return fallback
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 || days > 3650 {
		r.fail("%s must be between 1 and 3650 days", name)
		return fallback
	}
	return days
}
