-- Optional: what the station charged per gallon, as printed on the receipt.
-- Only stored for now; metrics come later.

-- +goose Up
ALTER TABLE fuel_logs
    ADD COLUMN price_per_gallon NUMERIC(8,2) CHECK (price_per_gallon > 0);

-- +goose Down
ALTER TABLE fuel_logs DROP COLUMN price_per_gallon;
