-- Optional photo of the pump receipt. The file lives on disk and is removed
-- after PHOTO_RETENTION_DAYS (same as trip and voucher photos); the fill-up stays.

-- +goose Up
ALTER TABLE fuel_logs
    ADD COLUMN photo_key          VARCHAR(255),
    ADD COLUMN photo_content_type VARCHAR(40),
    ADD COLUMN photo_size_bytes   INTEGER CHECK (photo_size_bytes > 0),
    ADD COLUMN photo_created_at   TIMESTAMPTZ,
    ADD CONSTRAINT fuel_logs_photo_complete CHECK (
        (photo_key IS NULL) = (photo_content_type IS NULL)
        AND (photo_key IS NULL) = (photo_size_bytes IS NULL)
        AND (photo_key IS NULL) = (photo_created_at IS NULL)
    );
CREATE INDEX fuel_logs_photo_created_idx ON fuel_logs (photo_created_at) WHERE photo_key IS NOT NULL;

-- +goose Down
DROP INDEX fuel_logs_photo_created_idx;
ALTER TABLE fuel_logs
    DROP CONSTRAINT fuel_logs_photo_complete,
    DROP COLUMN photo_created_at,
    DROP COLUMN photo_size_bytes,
    DROP COLUMN photo_content_type,
    DROP COLUMN photo_key;
