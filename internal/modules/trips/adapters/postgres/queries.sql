-- name: InsertTrip :exec
INSERT INTO trips (id, user_id, status, start_date, start_km, return_date, end_km, fuel_type,
                   fuel_rate, wear_rate, fuel_amount, wear_amount, total_amount, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14);

-- name: GetTrip :one
SELECT * FROM trips WHERE id = $1 AND user_id = $2;

-- Locks the trip until the transaction ends: two actions on it never interleave.
-- name: LockTrip :one
SELECT * FROM trips WHERE id = $1 AND user_id = $2 FOR UPDATE;

-- name: GetOpenTrip :one
SELECT * FROM trips WHERE user_id = $1 AND status = 'IN_PROGRESS';

-- name: ListTrips :many
SELECT * FROM trips
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY start_date DESC, created_at DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountTrips :one
SELECT count(*) FROM trips
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text);

-- name: ListTripsByStatus :many
SELECT * FROM trips
WHERE user_id = $1 AND status = ANY(sqlc.arg(statuses)::text[])
ORDER BY start_date DESC, created_at DESC
LIMIT sqlc.arg(row_limit);

-- name: ReturnTrip :exec
UPDATE trips
SET status = 'PENDING', return_date = $3, end_km = $4, fuel_type = $5, fuel_rate = $6,
    wear_rate = $7, fuel_amount = $8, wear_amount = $9, total_amount = $10, updated_at = now()
WHERE id = $1 AND user_id = $2;

-- name: SetTripStatus :exec
UPDATE trips SET status = $3, paid_date = $4, updated_at = now()
WHERE id = $1 AND user_id = $2;

-- name: PayTrips :many
UPDATE trips SET status = 'PAID', paid_date = $2, updated_at = now()
WHERE user_id = $1 AND status = 'PENDING' AND id = ANY(sqlc.arg(ids)::uuid[])
RETURNING id;

-- name: DeleteTrip :exec
DELETE FROM trips WHERE id = $1 AND user_id = $2;

-- What the car was lent for since a date (home's "last 30 days").
-- name: BillableSince :one
SELECT COALESCE(sum(end_km - start_km), 0)::numeric AS distance_km,
       COALESCE(sum(total_amount), 0)::numeric AS charged,
       count(*) AS trips
FROM trips
WHERE user_id = $1 AND status IN ('PENDING', 'PAID') AND start_date >= $2;

-- Fuel charged per month, to compare with what was spent on fuel.
-- name: FuelChargedByMonth :many
SELECT date_trunc('month', start_date)::date AS month,
       sum(fuel_amount)::numeric AS charged
FROM trips
WHERE user_id = $1 AND status IN ('PENDING', 'PAID') AND start_date >= $2
GROUP BY 1
ORDER BY 1 DESC;

-- The highest reading the trips know of.
-- name: MaxTripOdometer :one
SELECT COALESCE(max(GREATEST(start_km, COALESCE(end_km, 0))), 0)::numeric AS km
FROM trips
WHERE user_id = $1 AND status <> 'CANCELLED';

-- name: LastFuelType :one
SELECT fuel_type FROM trips
WHERE user_id = $1 AND fuel_type IS NOT NULL
ORDER BY start_date DESC, created_at DESC
LIMIT 1;

-- name: PhotosOfTrips :many
SELECT * FROM trip_photos WHERE trip_id = ANY(sqlc.arg(trip_ids)::uuid[]);

-- name: PhotosOfTrip :many
SELECT * FROM trip_photos WHERE trip_id = $1;

-- name: GetPhoto :one
SELECT * FROM trip_photos WHERE id = $1;

-- name: GetTripPhoto :one
SELECT * FROM trip_photos WHERE trip_id = $1 AND leg = $2;

-- name: DeleteTripPhoto :exec
DELETE FROM trip_photos WHERE id = $1;

-- name: InsertPhoto :exec
INSERT INTO trip_photos (id, trip_id, user_id, leg, storage_key, content_type, size_bytes, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- Photos past their retention, oldest first, a batch at a time.
-- name: ExpiredPhotos :many
SELECT * FROM trip_photos WHERE created_at < $1 ORDER BY created_at LIMIT $2;

-- name: InsertVoucher :exec
INSERT INTO vouchers (id, user_id, date, amount, created_at) VALUES ($1, $2, $3, $4, $5);

-- name: GetVoucher :one
SELECT * FROM vouchers WHERE id = $1 AND user_id = $2;

-- Serves a photo to whoever holds its signed URL: the signature is the credential.
-- name: GetVoucherByID :one
SELECT * FROM vouchers WHERE id = $1;

-- name: LockVoucher :one
SELECT * FROM vouchers WHERE id = $1 AND user_id = $2 FOR UPDATE;

-- name: ListVouchers :many
SELECT * FROM vouchers
WHERE user_id = sqlc.arg(user_id) AND (settled_date IS NOT NULL) = sqlc.arg(settled)::boolean
ORDER BY date DESC, created_at DESC
LIMIT sqlc.arg(row_limit);

-- name: SetVoucherPhoto :exec
UPDATE vouchers
SET photo_key = $3, photo_content_type = $4, photo_size_bytes = $5, photo_created_at = $6
WHERE id = $1 AND user_id = $2;

-- name: ClearVoucherPhoto :exec
UPDATE vouchers
SET photo_key = NULL, photo_content_type = NULL, photo_size_bytes = NULL, photo_created_at = NULL
WHERE id = $1;

-- name: DeleteVoucher :execrows
DELETE FROM vouchers WHERE id = $1 AND user_id = $2 AND settled_date IS NULL;

-- name: SettleVouchers :many
UPDATE vouchers SET settled_date = $2
WHERE user_id = $1 AND settled_date IS NULL AND id = ANY(sqlc.arg(ids)::uuid[])
RETURNING *;

-- Photos past their retention, oldest first.
-- name: ExpiredVoucherPhotos :many
SELECT * FROM vouchers WHERE photo_key IS NOT NULL AND photo_created_at < $1 ORDER BY photo_created_at LIMIT $2;
