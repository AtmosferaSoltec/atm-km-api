-- name: InsertFuelLog :exec
INSERT INTO fuel_logs (id, user_id, date, odometer, fuel_type, amount, is_full, price_per_gallon, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetFuelLog :one
SELECT * FROM fuel_logs WHERE id = $1 AND user_id = $2;

-- Serves a photo to whoever holds its signed URL: the signature is the credential.
-- name: GetFuelLogByID :one
SELECT * FROM fuel_logs WHERE id = $1;

-- name: LockFuelLog :one
SELECT * FROM fuel_logs WHERE id = $1 AND user_id = $2 FOR UPDATE;

-- name: UpdateFuelLog :execrows
UPDATE fuel_logs
SET date = $3, odometer = $4, fuel_type = $5, amount = $6, is_full = $7, price_per_gallon = $8
WHERE id = $1 AND user_id = $2;

-- name: SetFuelLogPhoto :exec
UPDATE fuel_logs
SET photo_key = $3, photo_content_type = $4, photo_size_bytes = $5, photo_created_at = $6
WHERE id = $1 AND user_id = $2;

-- name: ClearFuelLogPhoto :exec
UPDATE fuel_logs
SET photo_key = NULL, photo_content_type = NULL, photo_size_bytes = NULL, photo_created_at = NULL
WHERE id = $1;

-- Photos past their retention, oldest first.
-- name: ExpiredFuelPhotos :many
SELECT * FROM fuel_logs WHERE photo_key IS NOT NULL AND photo_created_at < $1 ORDER BY photo_created_at LIMIT $2;

-- name: DeleteFuelLog :execrows
DELETE FROM fuel_logs WHERE id = $1 AND user_id = $2;

-- name: ListFuelLogs :many
SELECT * FROM fuel_logs
WHERE user_id = $1
ORDER BY date DESC, created_at DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountFuelLogs :one
SELECT count(*) FROM fuel_logs WHERE user_id = $1;

-- name: FuelLogsSince :many
SELECT * FROM fuel_logs
WHERE user_id = $1 AND date >= $2
ORDER BY date DESC, created_at DESC;

-- name: HasFuelLogsBefore :one
SELECT EXISTS (SELECT 1 FROM fuel_logs WHERE user_id = $1 AND date < $2);

-- name: SpentSince :one
SELECT COALESCE(sum(amount), 0)::numeric FROM fuel_logs WHERE user_id = $1 AND date >= $2;

-- name: MaxFuelOdometer :one
SELECT COALESCE(max(odometer), 0)::numeric FROM fuel_logs WHERE user_id = $1;

-- Real cost per km: what was spent from the first fill-up up to the one before
-- the last, over the km between the first and the last. The last fill-up has
-- not been burned yet.
-- name: CostPerKmInputs :one
WITH ordered AS (
    SELECT odometer, amount,
           row_number() OVER (ORDER BY date, created_at) AS first_rank,
           row_number() OVER (ORDER BY date DESC, created_at DESC) AS last_rank
    FROM fuel_logs
    WHERE user_id = $1
)
SELECT count(*) AS logs,
       COALESCE(sum(amount) FILTER (WHERE last_rank > 1), 0)::numeric AS spent,
       COALESCE(max(odometer) FILTER (WHERE last_rank = 1), 0)::numeric AS last_odometer,
       COALESCE(max(odometer) FILTER (WHERE first_rank = 1), 0)::numeric AS first_odometer
FROM ordered;
