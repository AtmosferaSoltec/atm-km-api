package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/config"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/testsupport"
	"github.com/AtmosferaSoltec/atm-km-api/internal/server"
)

// These tests run the whole API, as main wires it, against km_test.

const password = "a-good-password"

// movableClock lets a test jump forward: token expiry, refresh windows, photo
// retention.
type movableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *movableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *movableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type env struct {
	t        *testing.T
	app      *server.App
	clock    *movableClock
	photoDir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testsupport.MigratedPool(t)
	gin.SetMode(gin.TestMode)
	photoDir := t.TempDir()
	vars := map[string]string{
		"APP_ENV": "test", "PORT": "3081", "DATABASE_URL": testsupport.DatabaseURL(t),
		"JWT_ACCESS_SECRET":           "test-secret-test-secret-test-secret-1234",
		"ACCESS_TOKEN_TTL_SECONDS":    "3600",
		"REFRESH_TOKEN_TTL_DAYS":      "90",
		"RATE_LIMIT_PER_MINUTE":       "10000",
		"LOGIN_RATE_LIMIT_PER_MINUTE": "10000",
		"PHOTOS_DIR":                  photoDir,
		"PHOTO_RETENTION_DAYS":        "90",
	}
	cfg, err := config.LoadFrom(func(name string) string { return vars[name] })
	require.NoError(t, err)
	// 15:00 in Lima: "today" is November 2 in both Lima and UTC.
	clock := &movableClock{now: time.Date(2026, 11, 2, 20, 0, 0, 0, time.UTC)}
	params := identity.FastPasswordParams
	app, err := server.Build(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{Clock: clock, PasswordParams: &params})
	require.NoError(t, err)
	return &env{t: t, app: app, clock: clock, photoDir: photoDir}
}

// newUser creates an account through the same use case as `api user create`.
func (e *env) newUser(label string) string {
	e.t.Helper()
	username := testsupport.UniqueUsername(label)
	testsupport.DeleteUsers(e.t, testsupport.Pool(e.t), username)
	_, err := e.app.Identity.CreateUser(context.Background(), identity.CreateUserInput{
		Username: username, DisplayName: "Joel " + label, Password: password,
	})
	require.NoError(e.t, err)
	return username
}

type response struct {
	Status int
	Body   map[string]any
	Header http.Header
	Raw    []byte
}

func (r response) data() map[string]any {
	data, _ := r.Body["data"].(map[string]any)
	return data
}

func (r response) errorCode() string {
	errBody, _ := r.Body["error"].(map[string]any)
	code, _ := errBody["code"].(string)
	return code
}

func (e *env) do(method, path, token string, body any, headers ...string) response {
	e.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
	default:
		encoded, err := json.Marshal(b)
		require.NoError(e.t, err)
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if _, raw := body.([]byte); !raw && body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.app.Router.ServeHTTP(rec, req)
	out := response{Status: rec.Code, Header: rec.Header(), Raw: rec.Body.Bytes()}
	_ = json.Unmarshal(out.Raw, &out.Body)
	return out
}

func (e *env) login(username string) (access, refresh string) {
	e.t.Helper()
	res := e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
	require.Equal(e.t, http.StatusOK, res.Status, string(res.Raw))
	tokens := res.data()["tokens"].(map[string]any)
	return tokens["access_token"].(string), tokens["refresh_token"].(string)
}

func TestLoginRefreshAndLogout(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("auth")

	bad := e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": username, "password": "nope-nope"})
	assert.Equal(t, http.StatusUnauthorized, bad.Status)
	assert.Equal(t, "INVALID_CREDENTIALS", bad.errorCode())
	unknown := e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": "nobody-here", "password": "nope-nope"})
	assert.Equal(t, "INVALID_CREDENTIALS", unknown.errorCode(), "same answer for unknown users")

	access, refresh := e.login(strings.ToUpper(" " + username + " "))
	me := e.do("GET", "/api/v1/me", access, nil)
	require.Equal(t, http.StatusOK, me.Status)
	assert.Equal(t, username, me.data()["username"])

	// The access token lives an hour.
	e.clock.Advance(61 * time.Minute)
	assert.Equal(t, http.StatusUnauthorized, e.do("GET", "/api/v1/me", access, nil).Status)

	// Refreshing rotates the refresh token.
	rotated := e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
	require.Equal(t, http.StatusOK, rotated.Status, string(rotated.Raw))
	tokens := rotated.data()["tokens"].(map[string]any)
	newAccess, newRefresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	assert.Equal(t, float64(3600), tokens["access_token_expires_in"])
	assert.Equal(t, http.StatusOK, e.do("GET", "/api/v1/me", newAccess, nil).Status)

	// The old one again, right away, is a parallel request: retry, no harm done.
	race := e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
	assert.Equal(t, http.StatusConflict, race.Status)
	assert.Equal(t, "REFRESH_TOKEN_ROTATED", race.errorCode())
	assert.Equal(t, http.StatusOK, e.do("GET", "/api/v1/me", newAccess, nil).Status)

	// Logout ends the session at once, access token included.
	assert.Equal(t, http.StatusNoContent, e.do("POST", "/api/v1/auth/logout", "", map[string]string{"refresh_token": newRefresh}).Status)
	assert.Equal(t, http.StatusUnauthorized, e.do("GET", "/api/v1/me", newAccess, nil).Status)
	assert.Equal(t, "SESSION_EXPIRED", e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": newRefresh}).errorCode())
}

func TestReusingAnOldRefreshTokenRevokesTheSession(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("reuse")
	_, refresh := e.login(username)

	rotated := e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
	require.Equal(t, http.StatusOK, rotated.Status)
	access := rotated.data()["tokens"].(map[string]any)["access_token"].(string)

	e.clock.Advance(time.Minute)
	stolen := e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
	assert.Equal(t, "SESSION_EXPIRED", stolen.errorCode())
	assert.Equal(t, http.StatusUnauthorized, e.do("GET", "/api/v1/me", access, nil).Status, "the whole session is gone")
}

func TestTheSessionSlidesNinetyDays(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("slide")
	_, refresh := e.login(username)

	// Opening the app every 80 days keeps the session alive...
	for range 3 {
		e.clock.Advance(80 * 24 * time.Hour)
		res := e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh})
		require.Equal(t, http.StatusOK, res.Status, string(res.Raw))
		refresh = res.data()["tokens"].(map[string]any)["refresh_token"].(string)
	}
	// ...and 90 days away asks to log in again.
	e.clock.Advance(90*24*time.Hour + time.Second)
	assert.Equal(t, "SESSION_EXPIRED", e.do("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh}).errorCode())
}

func TestWebSessionTravelsInAnHttpOnlyCookie(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("cookie")
	res := e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": username, "password": password}, "X-Session-Transport", "cookie")
	require.Equal(t, http.StatusOK, res.Status)
	assert.NotContains(t, res.data()["tokens"], "refresh_token", "JavaScript never sees it")
	cookie := res.Header.Get("Set-Cookie")
	assert.Contains(t, cookie, "km_refresh=")
	assert.Contains(t, cookie, "HttpOnly")
	assert.Contains(t, cookie, "SameSite=Strict")
	assert.Contains(t, cookie, "Path=/api/v1/auth")
	assert.Contains(t, cookie, "Max-Age=7776000")

	value := strings.SplitN(strings.TrimPrefix(cookie, "km_refresh="), ";", 2)[0]
	refreshed := e.do("POST", "/api/v1/auth/refresh", "", nil, "X-Session-Transport", "cookie", "Cookie", "km_refresh="+value)
	require.Equal(t, http.StatusOK, refreshed.Status, string(refreshed.Raw))
	assert.Contains(t, refreshed.Header.Get("Set-Cookie"), "km_refresh=")

	missing := e.do("POST", "/api/v1/auth/refresh", "", nil, "X-Session-Transport", "cookie")
	assert.Equal(t, "SESSION_EXPIRED", missing.errorCode())
	assert.Contains(t, missing.Header.Get("Set-Cookie"), "Max-Age=0", "a dead cookie is cleared")
}

func TestLoginBacksOffAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("backoff")
	for range 5 {
		e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": username, "password": "wrong-one"})
	}
	blocked := e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
	assert.Equal(t, http.StatusTooManyRequests, blocked.Status)
	assert.NotEmpty(t, blocked.Header.Get("Retry-After"))

	e.clock.Advance(time.Minute + time.Second)
	e.login(username)
}

func TestDeactivatingAUserCutsAccessAndTheShareLink(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("off")
	access, _ := e.login(username)
	token := e.do("GET", "/api/v1/settings", access, nil).data()["share_token"].(string)
	assert.Equal(t, http.StatusOK, e.do("GET", "/api/v1/share/"+token, "", nil).Status)

	require.NoError(t, e.app.Identity.SetActive(context.Background(), username, false))
	assert.Equal(t, http.StatusUnauthorized, e.do("GET", "/api/v1/me", access, nil).Status)
	assert.Equal(t, "SHARE_LINK_NOT_FOUND", e.do("GET", "/api/v1/share/"+token, "", nil).errorCode())
	assert.Equal(t, "INVALID_CREDENTIALS", e.do("POST", "/api/v1/auth/login", "", map[string]string{"username": username, "password": password}).errorCode())

	require.NoError(t, e.app.Identity.SetActive(context.Background(), username, true))
	e.login(username)
}

func TestLendReceiveAndGetPaid(t *testing.T) {
	e := newEnv(t)
	access, _ := e.login(e.newUser("trips"))

	settings := e.do("PUT", "/api/v1/settings/rates", access, map[string]string{"gasoline_rate": "0.30", "lpg_rate": "0.10", "wear_rate": "0.30"})
	require.Equal(t, http.StatusOK, settings.Status, string(settings.Raw))

	lent := e.do("POST", "/api/v1/trips", access, map[string]any{"start_date": "2026-10-28", "start_km": 45210.5})
	require.Equal(t, http.StatusCreated, lent.Status, string(lent.Raw))
	tripID := lent.data()["id"].(string)
	assert.Equal(t, "IN_PROGRESS", lent.data()["status"])

	twice := e.do("POST", "/api/v1/trips", access, map[string]any{"start_date": "2026-10-28", "start_km": 45210.5})
	assert.Equal(t, "CAR_ALREADY_LENT", twice.errorCode())

	open := e.do("GET", "/api/v1/trips/open", access, nil)
	assert.Equal(t, tripID, open.data()["id"])

	invalid := e.do("POST", "/api/v1/trips/"+tripID+"/return", access, map[string]any{"return_date": "2026-10-27", "end_km": 45000, "fuel_type": "LPG"})
	assert.Equal(t, "VALIDATION_FAILED", invalid.errorCode())

	returned := e.do("POST", "/api/v1/trips/"+tripID+"/return", access, map[string]any{"return_date": "2026-10-30", "end_km": "45382.5", "fuel_type": "LPG"})
	require.Equal(t, http.StatusOK, returned.Status, string(returned.Raw))
	trip := returned.data()
	assert.Equal(t, "PENDING", trip["status"])
	assert.Equal(t, float64(172), trip["distance_km"])
	assert.Equal(t, "0.100", trip["fuel_rate"])
	assert.Equal(t, "17.20", trip["fuel_amount"])
	assert.Equal(t, "51.60", trip["wear_amount"])
	assert.Equal(t, "68.80", trip["total_amount"])

	// New rates never change what was already charged.
	e.do("PUT", "/api/v1/settings/rates", access, map[string]string{"gasoline_rate": "1", "lpg_rate": "1", "wear_rate": "1"})
	assert.Equal(t, "68.80", e.do("GET", "/api/v1/trips/"+tripID, access, nil).data()["total_amount"])

	// A trip registered after the fact, with the new rates.
	late := e.do("POST", "/api/v1/trips", access, map[string]any{
		"start_date": "2026-11-01", "start_km": 45400,
		"return": map[string]any{"return_date": "2026-11-02", "end_km": 45410, "fuel_type": "GASOLINE"},
	})
	require.Equal(t, http.StatusCreated, late.Status, string(late.Raw))
	assert.Equal(t, "20.00", late.data()["total_amount"])

	home := e.do("GET", "/api/v1/home", access, nil).data()
	assert.Nil(t, home["open_trip"])
	pending := home["pending"].(map[string]any)
	assert.Equal(t, float64(2), pending["count"])
	assert.Equal(t, "88.80", pending["total_amount"])
	assert.Equal(t, float64(45410), home["last_odometer"])
	assert.Equal(t, "GASOLINE", home["last_fuel_type"])

	paid := e.do("POST", "/api/v1/trips/pay", access, map[string]any{"trip_ids": []string{tripID, late.data()["id"].(string)}})
	require.Equal(t, http.StatusOK, paid.Status, string(paid.Raw))
	assert.Equal(t, "88.80", paid.data()["summary"].(map[string]any)["total_amount"])
	again := e.do("POST", "/api/v1/trips/pay", access, map[string]any{"trip_ids": []string{tripID}})
	assert.Equal(t, "0.00", again.data()["summary"].(map[string]any)["total_amount"], "paying twice is harmless")

	trip = e.do("GET", "/api/v1/trips/"+tripID, access, nil).data()
	assert.Equal(t, "PAID", trip["status"])
	assert.Equal(t, "2026-11-02", trip["paid_date"])

	undone := e.do("POST", "/api/v1/trips/"+tripID+"/unpay", access, nil)
	assert.Equal(t, "PENDING", undone.data()["status"])
	assert.Nil(t, undone.data()["paid_date"])
	assert.Equal(t, "TRIP_STATUS_CONFLICT", e.do("POST", "/api/v1/trips/"+tripID+"/unpay", access, nil).errorCode())

	list := e.do("GET", "/api/v1/trips?status=PENDING&page_size=1", access, nil)
	assert.Len(t, list.Body["data"], 1)
	assert.Equal(t, float64(1), list.Body["meta"].(map[string]any)["total"])
}

func TestCancelAndDelete(t *testing.T) {
	e := newEnv(t)
	access, _ := e.login(e.newUser("cancel"))
	tripID := e.do("POST", "/api/v1/trips", access, map[string]any{"start_date": "2026-11-02", "start_km": 100}).data()["id"].(string)

	assert.Equal(t, "TRIP_STATUS_CONFLICT", e.do("DELETE", "/api/v1/trips/"+tripID, access, nil).errorCode(), "cancel first")
	assert.Equal(t, "CANCELLED", e.do("POST", "/api/v1/trips/"+tripID+"/cancel", access, nil).data()["status"])
	assert.Equal(t, http.StatusCreated, e.do("POST", "/api/v1/trips", access, map[string]any{"start_date": "2026-11-02", "start_km": 100}).Status, "the car is home again")
	assert.Equal(t, http.StatusNoContent, e.do("DELETE", "/api/v1/trips/"+tripID, access, nil).Status)
	assert.Equal(t, http.StatusNotFound, e.do("GET", "/api/v1/trips/"+tripID, access, nil).Status)
}

func TestUsersOnlySeeTheirOwnData(t *testing.T) {
	e := newEnv(t)
	ana, _ := e.login(e.newUser("ana"))
	beto, _ := e.login(e.newUser("beto"))

	tripID := e.do("POST", "/api/v1/trips", ana, map[string]any{"start_date": "2026-11-02", "start_km": 100}).data()["id"].(string)
	logID := e.do("POST", "/api/v1/fuel-logs", ana, map[string]any{"date": "2026-11-01", "odometer": 90, "fuel_type": "LPG", "amount": "30"}).data()["id"].(string)

	assert.Equal(t, http.StatusNotFound, e.do("GET", "/api/v1/trips/"+tripID, beto, nil).Status)
	assert.Equal(t, http.StatusNotFound, e.do("POST", "/api/v1/trips/"+tripID+"/cancel", beto, nil).Status)
	assert.Equal(t, http.StatusNotFound, e.do("DELETE", "/api/v1/fuel-logs/"+logID, beto, nil).Status)
	assert.Nil(t, e.do("GET", "/api/v1/trips/open", beto, nil).Body["data"])
	assert.Equal(t, http.StatusCreated, e.do("POST", "/api/v1/trips", beto, map[string]any{"start_date": "2026-11-02", "start_km": 5}).Status, "each user has their own car")
	assert.Empty(t, e.do("GET", "/api/v1/fuel-logs", beto, nil).Body["data"])
}

func TestFuelLogPricePerGallonIsOptional(t *testing.T) {
	e := newEnv(t)
	access, _ := e.login(e.newUser("price"))
	log := func(extra map[string]any) response {
		body := map[string]any{"date": "2026-11-01", "odometer": 100, "fuel_type": "GASOLINE", "amount": "35"}
		for k, v := range extra {
			body[k] = v
		}
		return e.do("POST", "/api/v1/fuel-logs", access, body)
	}

	without := log(nil)
	require.Equal(t, http.StatusCreated, without.Status)
	assert.Nil(t, without.data()["price_per_gallon"])

	with := log(map[string]any{"price_per_gallon": "5.80"})
	require.Equal(t, http.StatusCreated, with.Status)
	assert.Equal(t, "5.80", with.data()["price_per_gallon"])

	assert.Equal(t, http.StatusBadRequest, log(map[string]any{"price_per_gallon": "0"}).Status)
	assert.Equal(t, http.StatusBadRequest, log(map[string]any{"price_per_gallon": "5.805"}).Status)

	listed := e.do("GET", "/api/v1/fuel-logs", access, nil).Body["data"].([]any)
	prices := map[any]int{}
	for _, row := range listed {
		prices[row.(map[string]any)["price_per_gallon"]]++
	}
	assert.Equal(t, map[any]int{nil: 1, "5.80": 1}, prices)
}

func TestFuelSummary(t *testing.T) {
	e := newEnv(t)
	access, _ := e.login(e.newUser("fuel"))
	add := func(date string, odometer int, amount string) {
		res := e.do("POST", "/api/v1/fuel-logs", access, map[string]any{"date": date, "odometer": odometer, "fuel_type": "GASOLINE", "amount": amount})
		require.Equal(t, http.StatusCreated, res.Status, string(res.Raw))
	}
	add("2026-10-01", 1000, "50")
	add("2026-10-15", 1200, "40.5")
	add("2026-11-01", 1500, "45")
	e.do("POST", "/api/v1/trips", access, map[string]any{
		"start_date": "2026-10-20", "start_km": 1250,
		"return": map[string]any{"return_date": "2026-10-21", "end_km": 1350, "fuel_type": "GASOLINE"},
	})

	invalid := e.do("POST", "/api/v1/fuel-logs", access, map[string]any{"date": "2026-11-03", "odometer": 1, "fuel_type": "GASOLINE", "amount": "0"})
	assert.Equal(t, "VALIDATION_FAILED", invalid.errorCode())

	summary := e.do("GET", "/api/v1/fuel/summary?months=2", access, nil).data()
	months := summary["months"].([]any)
	require.Len(t, months, 2)
	november, october := months[0].(map[string]any), months[1].(map[string]any)
	assert.Equal(t, "2026-11", november["month"])
	assert.Equal(t, "45.00", november["spent"])
	assert.Equal(t, "2026-10", october["month"])
	assert.Equal(t, "90.50", october["spent"])
	assert.Equal(t, "30.00", october["charged"])
	assert.Len(t, october["logs"], 2)
	// (50 + 40.5) / (1500 - 1000): the last fill-up is not burned yet.
	assert.Equal(t, "0.181", summary["real_cost_per_km"])
	assert.Equal(t, false, summary["has_more"])

	one := e.do("GET", "/api/v1/fuel/summary?months=1", access, nil).data()
	assert.Len(t, one["months"], 1)
	assert.Equal(t, true, one["has_more"])
}

func TestPhotosAreServedSignedAndDeletedAfterRetention(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("photos")
	access, _ := e.login(username)
	tripID := e.do("POST", "/api/v1/trips", access, map[string]any{"start_date": "2026-11-02", "start_km": 100}).data()["id"].(string)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("x"), 500)...)

	assert.Equal(t, "TRIP_NOT_RETURNED", e.do("PUT", "/api/v1/trips/"+tripID+"/photos/end", access, jpeg).errorCode())
	assert.Equal(t, "VALIDATION_FAILED", e.do("PUT", "/api/v1/trips/"+tripID+"/photos/start", access, []byte("not an image")).errorCode())

	uploaded := e.do("PUT", "/api/v1/trips/"+tripID+"/photos/start", access, jpeg)
	require.Equal(t, http.StatusOK, uploaded.Status, string(uploaded.Raw))
	photo := uploaded.data()["photos"].(map[string]any)["start"].(map[string]any)
	assert.Equal(t, "2027-01-31T20:00:00Z", photo["expires_at"], "kept for 90 days")
	url := photo["url"].(string)

	served := e.do("GET", url, "", nil)
	require.Equal(t, http.StatusOK, served.Status)
	assert.Equal(t, "image/jpeg", served.Header.Get("Content-Type"))
	assert.Equal(t, jpeg, served.Raw)
	assert.Equal(t, http.StatusNotFound, e.do("GET", strings.Replace(url, "sig=", "sig=x", 1), "", nil).Status)

	// Replacing it leaves a single file.
	e.do("PUT", "/api/v1/trips/"+tripID+"/photos/start", access, jpeg)
	assert.Equal(t, 1, countFiles(t, e.photoDir))

	e.clock.Advance(13 * time.Hour)
	assert.Equal(t, http.StatusNotFound, e.do("GET", url, "", nil).Status, "signed URLs expire")

	_, err := e.app.Trips.CleanupPhotos(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, countFiles(t, e.photoDir), "not yet")

	e.clock.Advance(91 * 24 * time.Hour)
	_, err = e.app.Trips.CleanupPhotos(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, countFiles(t, e.photoDir))
	access, _ = e.login(username)
	assert.Nil(t, e.do("GET", "/api/v1/trips/"+tripID, access, nil).data()["photos"].(map[string]any)["start"])
}

func TestVouchersAreDiscountedFromAPayment(t *testing.T) {
	e := newEnv(t)
	username := e.newUser("vouchers")
	access, _ := e.login(username)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("x"), 500)...)
	lend := func(start, end int) string {
		res := e.do("POST", "/api/v1/trips", access, map[string]any{
			"start_date": "2026-11-01", "start_km": start,
			"return": map[string]any{"return_date": "2026-11-01", "end_km": end, "fuel_type": "GASOLINE"},
		})
		require.Equal(t, http.StatusCreated, res.Status, string(res.Raw))
		return res.data()["id"].(string)
	}
	voucher := func(amount string) response {
		return e.do("POST", "/api/v1/vouchers", access, map[string]any{"date": "2026-11-01", "amount": amount})
	}
	tripA, tripB := lend(100, 150), lend(150, 200) // 30.00 each

	assert.Equal(t, "VALIDATION_FAILED", voucher("0").errorCode())
	v1 := voucher("20.00").data()["id"].(string)
	v2 := voucher("15.00").data()["id"].(string)
	uploaded := e.do("PUT", "/api/v1/vouchers/"+v1+"/photo", access, jpeg)
	require.Equal(t, http.StatusOK, uploaded.Status, string(uploaded.Raw))
	url := uploaded.data()["photo"].(map[string]any)["url"].(string)
	served := e.do("GET", url, "", nil)
	require.Equal(t, http.StatusOK, served.Status)
	assert.Equal(t, jpeg, served.Raw)
	assert.Equal(t, http.StatusNotFound, e.do("GET", strings.Replace(url, "sig=", "sig=x", 1), "", nil).Status)
	assert.Equal(t, "VALIDATION_FAILED", e.do("PUT", "/api/v1/vouchers/"+v2+"/photo", access, []byte("nope")).errorCode())

	// 60.00 owed, 35.00 already paid in fuel: 25.00 left, for the owner and for the borrower.
	pending := e.do("GET", "/api/v1/home", access, nil).data()["pending"].(map[string]any)
	assert.Equal(t, "60.00", pending["total_amount"])
	assert.Equal(t, "35.00", pending["voucher_amount"])
	assert.Equal(t, "25.00", pending["balance"])
	assert.Len(t, pending["vouchers"], 2)
	token := e.do("GET", "/api/v1/settings", access, nil).data()["share_token"].(string)
	shared := e.do("GET", "/api/v1/share/"+token, "", nil).data()["pending"].(map[string]any)
	assert.Equal(t, "25.00", shared["balance"])
	assert.Len(t, shared["vouchers"], 2)

	// Vouchers worth more than the trips paid: nothing changes.
	exceed := e.do("POST", "/api/v1/trips/pay", access, map[string]any{"trip_ids": []string{tripA}, "voucher_ids": []string{v1, v2}})
	assert.Equal(t, http.StatusConflict, exceed.Status)
	assert.Equal(t, "VOUCHERS_EXCEED", exceed.errorCode())
	assert.Equal(t, "PENDING", e.do("GET", "/api/v1/trips/"+tripA, access, nil).data()["status"])
	assert.Len(t, e.do("GET", "/api/v1/vouchers", access, nil).Body["data"], 2)

	paid := e.do("POST", "/api/v1/trips/pay", access, map[string]any{"trip_ids": []string{tripA, tripB}, "voucher_ids": []string{v1, v2}})
	require.Equal(t, http.StatusOK, paid.Status, string(paid.Raw))
	sum := paid.data()["summary"].(map[string]any)
	assert.Equal(t, "60.00", sum["total_amount"])
	assert.Equal(t, "35.00", sum["voucher_amount"])
	assert.Equal(t, "25.00", sum["net_amount"])
	assert.Empty(t, e.do("GET", "/api/v1/vouchers", access, nil).Body["data"])
	assert.Len(t, e.do("GET", "/api/v1/vouchers?settled=true", access, nil).Body["data"], 2)

	// A settled voucher is history: it cannot be deleted or change its photo.
	assert.Equal(t, "VOUCHER_SETTLED", e.do("DELETE", "/api/v1/vouchers/"+v1, access, nil).errorCode())
	assert.Equal(t, "VOUCHER_SETTLED", e.do("PUT", "/api/v1/vouchers/"+v1+"/photo", access, jpeg).errorCode())

	// An unsettled voucher takes its photo with it when deleted.
	v3 := voucher("10.00").data()["id"].(string)
	e.do("PUT", "/api/v1/vouchers/"+v3+"/photo", access, jpeg)
	assert.Equal(t, 2, countFiles(t, e.photoDir))
	assert.Equal(t, http.StatusNoContent, e.do("DELETE", "/api/v1/vouchers/"+v3, access, nil).Status)
	assert.Equal(t, 1, countFiles(t, e.photoDir))

	// Photos are cleaned up after the retention; the voucher stays.
	e.clock.Advance(91 * 24 * time.Hour)
	_, err := e.app.Trips.CleanupPhotos(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, countFiles(t, e.photoDir))
	access, _ = e.login(username)
	for _, row := range e.do("GET", "/api/v1/vouchers?settled=true", access, nil).Body["data"].([]any) {
		assert.Nil(t, row.(map[string]any)["photo"])
	}
}

func TestShareLink(t *testing.T) {
	e := newEnv(t)
	access, _ := e.login(e.newUser("share"))
	e.do("PUT", "/api/v1/settings/profile", access, map[string]string{"car_name": "Toyota Yaris", "borrower_name": "Carlos"})
	e.do("POST", "/api/v1/trips", access, map[string]any{
		"start_date": "2026-11-01", "start_km": 100,
		"return": map[string]any{"return_date": "2026-11-01", "end_km": 150, "fuel_type": "GASOLINE"},
	})
	token := e.do("GET", "/api/v1/settings", access, nil).data()["share_token"].(string)

	page := e.do("GET", "/api/v1/share/"+token, "", nil)
	require.Equal(t, http.StatusOK, page.Status)
	data := page.data()
	assert.Equal(t, "Toyota Yaris", data["car_name"])
	assert.Equal(t, "Carlos", data["borrower_name"])
	assert.Equal(t, "30.00", data["pending"].(map[string]any)["total_amount"])
	assert.NotContains(t, string(page.Raw), "fuel_logs")

	rotated := e.do("POST", "/api/v1/settings/share-token", access, nil).data()["share_token"].(string)
	assert.NotEqual(t, token, rotated)
	assert.Equal(t, "SHARE_LINK_NOT_FOUND", e.do("GET", "/api/v1/share/"+token, "", nil).errorCode())
	assert.Equal(t, http.StatusOK, e.do("GET", "/api/v1/share/"+rotated, "", nil).Status)
}

func TestProtectedRoutesNeedAToken(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/v1/me", "/api/v1/home", "/api/v1/trips", "/api/v1/settings", "/api/v1/fuel/summary"} {
		res := e.do("GET", path, "", nil)
		assert.Equal(t, http.StatusUnauthorized, res.Status, path)
		assert.Equal(t, "UNAUTHORIZED", res.errorCode(), path)
	}
	assert.Equal(t, http.StatusUnauthorized, e.do("GET", "/api/v1/me", "not-a-jwt", nil).Status)
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			count++
		}
		return err
	})
	require.NoError(t, err)
	return count
}
