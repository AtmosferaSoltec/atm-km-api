-- Car loans, billed per km, and their odometer photos.

-- +goose Up
CREATE TABLE trips (
    id           UUID         PRIMARY KEY,
    user_id      UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status       VARCHAR(12)  NOT NULL,
    start_date   DATE         NOT NULL,
    start_km     NUMERIC(9,1) NOT NULL CHECK (start_km >= 0),
    return_date  DATE,
    end_km       NUMERIC(9,1),
    fuel_type    VARCHAR(10),
    -- Rates frozen when the car comes back.
    fuel_rate    NUMERIC(8,3),
    wear_rate    NUMERIC(8,3),
    fuel_amount  NUMERIC(10,2),
    wear_amount  NUMERIC(10,2),
    total_amount NUMERIC(10,2),
    paid_date    DATE,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT trips_status_check CHECK (status IN ('IN_PROGRESS', 'PENDING', 'PAID', 'CANCELLED')),
    CONSTRAINT trips_fuel_type_check CHECK (fuel_type IN ('GASOLINE', 'LPG')),
    -- A returned trip has everything needed to bill it; an open or cancelled one has none of it.
    CONSTRAINT trips_return_complete CHECK (
        CASE WHEN status IN ('PENDING', 'PAID')
            THEN return_date IS NOT NULL AND end_km IS NOT NULL AND fuel_type IS NOT NULL
                 AND fuel_rate IS NOT NULL AND wear_rate IS NOT NULL AND fuel_amount IS NOT NULL
                 AND wear_amount IS NOT NULL AND total_amount IS NOT NULL
            ELSE return_date IS NULL AND end_km IS NULL AND total_amount IS NULL
        END
    ),
    CONSTRAINT trips_end_after_start CHECK (end_km IS NULL OR end_km > start_km),
    CONSTRAINT trips_return_after_start CHECK (return_date IS NULL OR return_date >= start_date),
    CONSTRAINT trips_paid_date CHECK ((status = 'PAID') = (paid_date IS NOT NULL))
);
CREATE INDEX trips_user_start_idx ON trips (user_id, start_date DESC, created_at DESC);
CREATE INDEX trips_user_status_idx ON trips (user_id, status);
-- The car can only be lent once at a time, even if two requests arrive together.
CREATE UNIQUE INDEX trips_one_in_progress_per_user ON trips (user_id) WHERE status = 'IN_PROGRESS';

-- Files live on disk; this is the index of them. Rows (and files) older than
-- PHOTO_RETENTION_DAYS are removed by the cleanup job.
CREATE TABLE trip_photos (
    id           UUID         PRIMARY KEY,
    trip_id      UUID         NOT NULL REFERENCES trips (id) ON DELETE CASCADE,
    user_id      UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    leg          VARCHAR(5)   NOT NULL CHECK (leg IN ('START', 'END')),
    storage_key  VARCHAR(255) NOT NULL,
    content_type VARCHAR(40)  NOT NULL,
    size_bytes   INTEGER      NOT NULL CHECK (size_bytes > 0),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX trip_photos_trip_leg_key ON trip_photos (trip_id, leg);
CREATE INDEX trip_photos_created_idx ON trip_photos (created_at);

-- +goose Down
DROP TABLE trip_photos;
DROP TABLE trips;
