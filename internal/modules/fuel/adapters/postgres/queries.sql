-- name: InsertFuelLog :exec
INSERT INTO fuel_logs (id, user_id, date, odometer, fuel_type, amount, is_full, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

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
