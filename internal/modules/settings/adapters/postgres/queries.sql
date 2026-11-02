-- name: EnsureSettings :exec
INSERT INTO user_settings (user_id) VALUES ($1)
ON CONFLICT (user_id) DO NOTHING;

-- name: GetSettings :one
SELECT user_id, car_name, borrower_name, gasoline_rate, lpg_rate, wear_rate, share_token
FROM user_settings
WHERE user_id = $1;

-- name: UpdateRates :one
UPDATE user_settings
SET gasoline_rate = $2, lpg_rate = $3, wear_rate = $4, updated_at = now()
WHERE user_id = $1
RETURNING user_id, car_name, borrower_name, gasoline_rate, lpg_rate, wear_rate, share_token;

-- name: UpdateProfile :one
UPDATE user_settings
SET car_name = $2, borrower_name = $3, updated_at = now()
WHERE user_id = $1
RETURNING user_id, car_name, borrower_name, gasoline_rate, lpg_rate, wear_rate, share_token;

-- name: RotateShareToken :one
UPDATE user_settings
SET share_token = $2, updated_at = now()
WHERE user_id = $1
RETURNING user_id, car_name, borrower_name, gasoline_rate, lpg_rate, wear_rate, share_token;

-- name: GetSettingsByShareToken :one
SELECT user_id, car_name, borrower_name, gasoline_rate, lpg_rate, wear_rate, share_token
FROM user_settings
WHERE share_token = $1;
