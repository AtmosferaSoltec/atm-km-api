-- Fuel the owner paid for. Private: never shown on the share link.

-- +goose Up
CREATE TABLE fuel_logs (
    id         UUID          PRIMARY KEY,
    user_id    UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    date       DATE          NOT NULL,
    odometer   NUMERIC(9,1)  NOT NULL CHECK (odometer >= 0),
    fuel_type  VARCHAR(10)   NOT NULL CHECK (fuel_type IN ('GASOLINE', 'LPG')),
    amount     NUMERIC(10,2) NOT NULL CHECK (amount > 0),
    -- "Fill it up": the pump sets the amount, so it is rarely round.
    is_full    BOOLEAN       NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now()
);
CREATE INDEX fuel_logs_user_date_idx ON fuel_logs (user_id, date DESC, created_at DESC);

-- +goose Down
DROP TABLE fuel_logs;
