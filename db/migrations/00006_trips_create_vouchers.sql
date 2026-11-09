-- Fuel vouchers the borrower paid while using the car. The owner registers
-- them; they are credit against what the borrower owes until they are settled
-- together with the trips they cover.

-- +goose Up
CREATE TABLE vouchers (
    id                 UUID          PRIMARY KEY,
    user_id            UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    date               DATE          NOT NULL,
    amount             NUMERIC(10,2) NOT NULL CHECK (amount > 0),
    -- Set when the voucher is discounted from a payment.
    settled_date       DATE,
    -- The photo of the receipt (optional). The file lives on disk and is removed
    -- after PHOTO_RETENTION_DAYS; the voucher itself stays.
    photo_key          VARCHAR(255),
    photo_content_type VARCHAR(40),
    photo_size_bytes   INTEGER CHECK (photo_size_bytes > 0),
    photo_created_at   TIMESTAMPTZ,
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CONSTRAINT vouchers_photo_complete CHECK (
        (photo_key IS NULL) = (photo_content_type IS NULL)
        AND (photo_key IS NULL) = (photo_size_bytes IS NULL)
        AND (photo_key IS NULL) = (photo_created_at IS NULL)
    )
);
CREATE INDEX vouchers_user_idx ON vouchers (user_id, settled_date, date DESC, created_at DESC);
CREATE INDEX vouchers_photo_created_idx ON vouchers (photo_created_at) WHERE photo_key IS NOT NULL;

-- +goose Down
DROP TABLE vouchers;
